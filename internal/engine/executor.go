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
	"syscall"
	"time"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

func unescapeInput(s string) string {
	s = strings.ReplaceAll(s, "\\n", "\n")
	s = strings.ReplaceAll(s, "\\t", "\t")
	s = strings.ReplaceAll(s, "\\r", "\r")
	return s
}

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return cmd.Process.Kill()
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
	configDir := p.EffectiveConfigDir()

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

	cmd, valgrindLogPath, preflightFailure := e.executionCommand(ctx, binaryDir, binaryPath, resolvedArgs)
	if preflightFailure != nil {
		return domain.NewExecuteResult(false, "", preflightFailure.Message, 0, false, args, input).WithValgrind(preflightFailure)
	}
	configureProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
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
	timedOut := ctx.Err() == context.DeadlineExceeded

	runOK := true
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			runOK = false
			if stderr.Len() == 0 {
				stderr.WriteString(err.Error())
			}
		}
	}

	return domain.NewExecuteResult(runOK, stdout.String(), stderr.String(), duration, timedOut, args, input).
		WithValgrind(e.valgrindResultFromLog(valgrindLogPath))
}

func (e *Executor) ExecuteTestCase(ctx context.Context, sub domain.Submission, tc policy.TestCase) domain.ExecuteResult {
	result := e.Execute(ctx, sub, tc.Args, tc.Input).WithTestCase(tc.Name)

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

func (e *Executor) executionCommand(ctx context.Context, binaryDir, binaryPath string, args []string) (*exec.Cmd, string, *domain.ValgrindResult) {
	valgrindPath, err := exec.LookPath("valgrind")
	if err != nil {
		return nil, "", domain.NewValgrindMissingResult("valgrind")
	}

	logFile, err := os.CreateTemp(binaryDir, "valgrind-*.log")
	if err != nil {
		return nil, "", domain.NewValgrindFailureResult(fmt.Sprintf("Could not create Valgrind log file: %v", err))
	}
	logPath := logFile.Name()
	_ = logFile.Close()

	valgrindArgs := []string{
		"--error-exitcode=97",
		"--log-file=" + logPath,
		"--dsymutil=yes",
		"--track-origins=yes",
		"--leak-check=full",
		"--track-fds=yes",
		"--show-reachable=yes",
		"-s",
		binaryPath,
	}
	valgrindArgs = append(valgrindArgs, args...)

	return exec.CommandContext(ctx, valgrindPath, valgrindArgs...), logPath, nil
}

func (e *Executor) valgrindResultFromLog(logPath string) *domain.ValgrindResult {
	if logPath == "" {
		return nil
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return domain.NewValgrindFailureResult(fmt.Sprintf("Could not read Valgrind log file: %v", err))
	}
	return domain.ParseValgrindLog(string(data), logPath)
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

		if scenario != nil {
			if scenarioArgs, ok := scenario.ProcessArgs[proc.Name]; ok {
				args = scenarioArgs
			}
			if scenarioInput, ok := scenario.ProcessInputs[proc.Name]; ok {
				input = scenarioInput
			}
		}

		args = e.resolveTestFilePaths(args)

		procResult := &domain.ProcessResult{
			Name:       proc.Name,
			SourceFile: proc.SourceFile,
			Running:    true,
			StartedAt:  time.Now(),
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
				procResult.Stderr = fmt.Sprintf("Binary not found: %s (compilation may have failed)", binaryPath)
				return
			}

			cmd, valgrindLogPath, preflightFailure := e.executionCommand(ctx, binaryDir, binaryPath, args)
			if preflightFailure != nil {
				procResult.Running = false
				procResult.Stderr = preflightFailure.Message
				procResult.Valgrind = preflightFailure
				return
			}
			configureProcessGroup(cmd)
			cmd.Cancel = func() error { return killProcessGroup(cmd) }
			cmd.Dir = binaryDir
			if input != "" {
				cmd.Stdin = strings.NewReader(unescapeInput(input))
			}

			stdoutPipe, _ := cmd.StdoutPipe()
			stderrPipe, _ := cmd.StderrPipe()

			if err := cmd.Start(); err != nil {
				procResult.Running = false
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
					_ = killProcessGroup(cmd)
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
					_ = killProcessGroup(cmd)
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
			if ctx.Err() == context.DeadlineExceeded {
				procResult.TimedOut = true
				procResult.Killed = true
			}

			runOK := true
			if err != nil {
				if _, ok := err.(*exec.ExitError); !ok {
					runOK = false
					if procResult.Stderr == "" {
						procResult.Stderr = err.Error()
					}
				}
			}

			procResult.Passed = runOK && !procResult.Killed

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
			if procResult.OutputMatch == domain.OutputMatchFail || procResult.OutputMatch == domain.OutputMatchMissing {
				procResult.Passed = false
			}
			procResult.Valgrind = e.valgrindResultFromLog(valgrindLogPath)
			if procResult.Valgrind != nil && procResult.Valgrind.Fails() {
				procResult.Passed = false
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
