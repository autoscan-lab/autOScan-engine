package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/felitrejos/autoscan-engine/pkg/domain"
	"github.com/felitrejos/autoscan-engine/pkg/policy"
)

func unescapeInput(s string) string {
	s = strings.ReplaceAll(s, "\\n", "\n")
	s = strings.ReplaceAll(s, "\\t", "\t")
	s = strings.ReplaceAll(s, "\\r", "\r")
	return s
}

type Executor struct {
	policy             *policy.Policy
	binaryDir          string
	outputName         string
	testFilesDir       string
	expectedOutputsDir string
	shortNames         bool
}

func NewExecutorWithOptions(p *policy.Policy, binaryDir string, shortNames bool) *Executor {
	outputName := strings.TrimSuffix(p.Compile.SourceFile, ".c")

	configDir, err := policy.ConfigDir()
	if err != nil {
		configDir = filepath.Join(".", ".autoscan")
	}
	return &Executor{
		policy:             p,
		binaryDir:          binaryDir,
		outputName:         outputName,
		testFilesDir:       filepath.Join(configDir, "test_files"),
		expectedOutputsDir: filepath.Join(configDir, "expected_outputs"),
		shortNames:         shortNames,
	}
}

func (e *Executor) submissionDirName(sub domain.Submission) string {
	dirName := sub.ID
	if e.shortNames {
		if idx := strings.Index(dirName, "_"); idx > 0 {
			dirName = dirName[:idx]
		}
	}
	return dirName
}

func (e *Executor) GetBinaryPath(sub domain.Submission) string {
	return filepath.Join(e.binaryDir, e.submissionDirName(sub), e.outputName)
}

func (e *Executor) GetSubmissionBinaryDir(sub domain.Submission) string {
	return filepath.Join(e.binaryDir, e.submissionDirName(sub))
}

func (e *Executor) Execute(ctx context.Context, sub domain.Submission, args []string, input string) domain.ExecuteResult {
	binaryPath := e.GetBinaryPath(sub)
	binaryDir := filepath.Dir(binaryPath)
	resolvedArgs := e.resolveTestFilePaths(args)

	cmd := exec.CommandContext(ctx, binaryPath, resolvedArgs...)
	cmd.Dir = binaryDir
	if input != "" {
		cmd.Stdin = strings.NewReader(unescapeInput(input))
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)
	timedOut := false

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	return domain.NewExecuteResult(exitCode >= 0 || err == nil, exitCode, stdout.String(), stderr.String(), duration, timedOut, args, input)
}

func (e *Executor) ExecuteTestCase(ctx context.Context, sub domain.Submission, tc policy.TestCase) domain.ExecuteResult {
	result := e.Execute(ctx, sub, tc.Args, tc.Input).WithTestCase(tc.Name, tc.ExpectedExit)

	// Compare output if expected output file is configured
	if tc.ExpectedOutputFile != "" {
		expectedPath := filepath.Join(e.expectedOutputsDir, tc.ExpectedOutputFile)
		if expectedData, err := os.ReadFile(expectedPath); err == nil {
			result.OutputMatch, result.OutputDiff = domain.ComputeOutputDiff(string(expectedData), result.Stdout)
		} else {
			result.OutputMatch = domain.OutputMatchMissing
		}
	} else {
		result.OutputMatch = domain.OutputMatchNone
	}

	return result
}

func (e *Executor) HasMultiProcess() bool {
	return e.policy.Run.MultiProcess != nil && e.policy.Run.MultiProcess.Enabled
}

// resolveTestFilePaths replaces args that exactly match a declared test file
// with their full path in ~/.config/autoscan/test_files/
func (e *Executor) resolveTestFilePaths(args []string) []string {
	if len(e.policy.TestFiles) == 0 {
		return args
	}

	testFileSet := make(map[string]bool, len(e.policy.TestFiles))
	for _, tf := range e.policy.TestFiles {
		testFileSet[tf] = true
	}

	resolved := make([]string, len(args))
	for i, arg := range args {
		if testFileSet[arg] {
			resolved[i] = filepath.Join(e.testFilesDir, arg)
		} else {
			resolved[i] = arg
		}
	}
	return resolved
}

func (e *Executor) ExecuteMultiProcess(ctx context.Context, sub domain.Submission, onUpdate func(*domain.MultiProcessResult)) *domain.MultiProcessResult {
	return e.executeMultiProcessWithOverrides(ctx, sub, nil, onUpdate)
}

func (e *Executor) ExecuteMultiProcessScenario(ctx context.Context, sub domain.Submission, scenario policy.MultiProcessScenario, onUpdate func(*domain.MultiProcessResult)) *domain.MultiProcessResult {
	return e.executeMultiProcessWithOverrides(ctx, sub, &scenario, onUpdate)
}

// executeMultiProcessWithOverrides spawns each executable as a process.
// Each process gets its own goroutine for lifecycle management, plus goroutines for
// stdout/stderr streaming. Processes run concurrently and can be cancelled via context.
func (e *Executor) executeMultiProcessWithOverrides(ctx context.Context, sub domain.Submission, scenario *policy.MultiProcessScenario, onUpdate func(*domain.MultiProcessResult)) *domain.MultiProcessResult {
	config := e.policy.Run.MultiProcess
	if config == nil || len(config.Executables) == 0 {
		return nil
	}

	result := domain.NewMultiProcessResult()
	if scenario != nil {
		result.ScenarioName = scenario.Name
	}
	start := time.Now()

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, proc := range config.Executables {
		wg.Add(1)

		args := proc.Args
		input := proc.Input
		var expectedExit *int

		if scenario != nil {
			if scenarioArgs, ok := scenario.ProcessArgs[proc.Name]; ok {
				args = scenarioArgs
			}
			if scenarioInput, ok := scenario.ProcessInputs[proc.Name]; ok {
				input = scenarioInput
			}
			if exit, ok := scenario.ExpectedExits[proc.Name]; ok {
				exitCopy := exit
				expectedExit = &exitCopy
			}
		}

		args = e.resolveTestFilePaths(args)

		procResult := &domain.ProcessResult{
			Name:         proc.Name,
			SourceFile:   proc.SourceFile,
			Running:      true,
			StartedAt:    time.Now(),
			ExpectedExit: expectedExit,
		}

		mu.Lock()
		result.AddProcess(proc.Name, procResult)
		mu.Unlock()

		go func(proc policy.ProcessConfig, args []string, input string, procResult *domain.ProcessResult) {
			defer wg.Done()

			if proc.StartDelayMs > 0 {
				select {
				case <-time.After(time.Duration(proc.StartDelayMs) * time.Millisecond):
				case <-ctx.Done():
					procResult.Killed = true
					procResult.Running = false
					return
				}
			}

			procResult.StartedAt = time.Now()

			binaryDir := e.GetSubmissionBinaryDir(sub)
			binaryPath := filepath.Join(binaryDir, strings.TrimSuffix(proc.SourceFile, ".c"))

			if _, err := os.Stat(binaryPath); err != nil {
				procResult.Running = false
				procResult.ExitCode = -1
				procResult.Stderr = fmt.Sprintf("Binary not found: %s (compilation may have failed)", binaryPath)
				return
			}

			cmd := exec.CommandContext(ctx, binaryPath, args...)
			cmd.Dir = binaryDir
			if input != "" {
				cmd.Stdin = strings.NewReader(unescapeInput(input))
			}

			stdoutPipe, _ := cmd.StdoutPipe()
			stderrPipe, _ := cmd.StderrPipe()

			if err := cmd.Start(); err != nil {
				procResult.Running = false
				procResult.ExitCode = -1
				procResult.Stderr = err.Error()
				return
			}

			if onUpdate != nil {
				mu.Lock()
				result.TotalDuration = time.Since(start)
				computeMultiProcessStatus(result)
				onUpdate(result)
				mu.Unlock()
			}

			var stdoutDone, stderrDone sync.WaitGroup
			stdoutDone.Add(1)
			stderrDone.Add(1)

			// Cancellation watcher: kills process and closes pipes immediately on cancel
			go func() {
				<-ctx.Done()
				if cmd.Process != nil {
					cmd.Process.Kill()
				}
				stdoutPipe.Close()
				stderrPipe.Close()
			}()

			// Stdout reader: streams output to result in real-time
			go func() {
				defer stdoutDone.Done()
				buf := make([]byte, 1024)
				for {
					n, err := stdoutPipe.Read(buf)
					if n > 0 {
						mu.Lock()
						procResult.Stdout += string(buf[:n])
						result.TotalDuration = time.Since(start)
						computeMultiProcessStatus(result)
						mu.Unlock()
						if onUpdate != nil {
							mu.Lock()
							onUpdate(result)
							mu.Unlock()
						}
					}
					if err != nil {
						break
					}
				}
			}()

			// Stderr reader: same pattern as stdout
			go func() {
				defer stderrDone.Done()
				buf := make([]byte, 1024)
				for {
					n, err := stderrPipe.Read(buf)
					if n > 0 {
						mu.Lock()
						procResult.Stderr += string(buf[:n])
						result.TotalDuration = time.Since(start)
						computeMultiProcessStatus(result)
						mu.Unlock()
						if onUpdate != nil {
							mu.Lock()
							onUpdate(result)
							mu.Unlock()
						}
					}
					if err != nil {
						break
					}
				}
			}()

			// Done waiter: waits for readers to finish, then sends exit status to channel.
			// Runs in goroutine so we can select on it alongside ctx.Done().
			done := make(chan error, 1)
			go func() {
				stdoutDone.Wait()
				stderrDone.Wait()
				done <- cmd.Wait()
			}()

			// Wait for process to finish OR context cancellation
			var err error
			select {
			case err = <-done:
			case <-ctx.Done():
				if cmd.Process != nil {
					cmd.Process.Kill()
				}
				err = <-done
				procResult.Killed = true
			}

			procResult.FinishedAt = time.Now()
			procResult.Duration = procResult.FinishedAt.Sub(procResult.StartedAt)
			procResult.Running = false

			if ctx.Err() == context.Canceled {
				procResult.Killed = true
			}

			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					procResult.ExitCode = exitErr.ExitCode()
				} else {
					procResult.ExitCode = -1
				}
			}

			if procResult.ExpectedExit != nil {
				procResult.Passed = !procResult.Killed && procResult.ExitCode == *procResult.ExpectedExit
			} else {
				procResult.Passed = !procResult.Killed
			}

			// Compare output if expected output file is configured for this process
			if scenario != nil && scenario.ExpectedOutputs != nil {
				if expectedFile, ok := scenario.ExpectedOutputs[proc.Name]; ok && expectedFile != "" {
					expectedPath := filepath.Join(e.expectedOutputsDir, expectedFile)
					if expectedData, err := os.ReadFile(expectedPath); err == nil {
						procResult.OutputMatch, procResult.OutputDiff = domain.ComputeOutputDiff(string(expectedData), procResult.Stdout)
					} else {
						procResult.OutputMatch = domain.OutputMatchMissing
					}
				} else {
					procResult.OutputMatch = domain.OutputMatchNone
				}
			} else {
				procResult.OutputMatch = domain.OutputMatchNone
			}

			if onUpdate != nil {
				mu.Lock()
				result.TotalDuration = time.Since(start)
				computeMultiProcessStatus(result)
				onUpdate(result)
				mu.Unlock()
			}
		}(proc, args, input, procResult)
	}

	wg.Wait()

	result.TotalDuration = time.Since(start)
	computeMultiProcessStatus(result)

	return result
}

func computeMultiProcessStatus(result *domain.MultiProcessResult) {
	allDone := true
	allPassed := true
	for _, pr := range result.Processes {
		if pr.Running {
			allDone = false
		}
		if pr.Killed {
			allPassed = false
		} else if !pr.Passed {
			allPassed = false
		}
	}
	result.AllCompleted = allDone
	result.AllPassed = allPassed
}
