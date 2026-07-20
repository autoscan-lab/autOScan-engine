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

// DefaultExecTimeout caps how long a single submission process may run.
const DefaultExecTimeout = 10 * time.Second

// MinimalEnv returns a scrubbed environment for child processes, so untrusted
// submission code cannot inherit the server's secrets.
func MinimalEnv(homeDir string) []string {
	return []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + homeDir,
		"LANG=C",
		"LC_ALL=C",
	}
}

// maxCapturedOutput caps buffered stdout/stderr per process so a submission
// cannot exhaust server memory by printing without bound.
const maxCapturedOutput = 1 << 20

// cappedBuffer collects output up to limit bytes, then drops the rest.
type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
			c.truncated = true
		} else {
			c.buf.Write(p)
		}
	} else if len(p) > 0 {
		c.truncated = true
	}
	return len(p), nil // report a full write so the process is not killed
}

func (c *cappedBuffer) WriteString(s string) (int, error) { return c.Write([]byte(s)) }

func (c *cappedBuffer) Len() int { return c.buf.Len() }

func (c *cappedBuffer) String() string {
	if c.truncated {
		return c.buf.String() + "\n... [output truncated]"
	}
	return c.buf.String()
}

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

// crashReasonFromExit describes how a process was killed by a fatal signal, or
// returns "" if it was not. A timeout kill is not treated as a crash. The
// sandbox chain reports signals as a 128+signal exit code.
func crashReasonFromExit(err error, timedOut bool) string {
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		return ""
	}
	sig := 0
	if code := exitErr.ExitCode(); code > 128 {
		sig = code - 128
	} else if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		sig = int(ws.Signal())
	}
	switch syscall.Signal(sig) {
	case syscall.SIGSEGV:
		return "segmentation fault"
	case syscall.SIGABRT:
		return "aborted"
	case syscall.SIGFPE:
		return "arithmetic exception"
	case syscall.SIGBUS:
		return "bus error"
	case syscall.SIGILL:
		return "illegal instruction"
	case syscall.SIGKILL:
		if timedOut {
			return ""
		}
		return "out of memory"
	}
	return ""
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

	ctx, cancel := context.WithTimeout(ctx, DefaultExecTimeout)
	defer cancel()

	cmd, valgrindLogPath, cleanup, preflightFailure := e.executionCommand(ctx, binaryDir, binaryPath, resolvedArgs)
	defer cleanup()
	if preflightFailure != nil {
		return domain.NewExecuteResult(false, "", preflightFailure.Message, 0, false, args, input).WithValgrind(preflightFailure)
	}
	configureProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.Dir = binaryDir
	cmd.Env = MinimalEnv(binaryDir)
	if input != "" {
		cmd.Stdin = strings.NewReader(unescapeInput(input))
	}

	stdout := &cappedBuffer{limit: maxCapturedOutput}
	stderr := &cappedBuffer{limit: maxCapturedOutput}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

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
		WithValgrind(e.valgrindResultFromLog(valgrindLogPath)).
		WithCrash(crashReasonFromExit(err, timedOut))
}

func (e *Executor) ExecuteTestCase(ctx context.Context, sub domain.Submission, tc policy.TestCase) domain.ExecuteResult {
	result := e.Execute(ctx, sub, tc.Args, tc.Input).WithTestCase(tc.Name)

	if tc.ExpectedOutputFile == "" {
		result.OutputMatch = domain.OutputMatchNone
		return result
	}

	expectedPath := filepath.Join(e.expectedOutputsDir, tc.ExpectedOutputFile)
	expectedData, err := os.ReadFile(expectedPath)
	if err != nil {
		result.OutputMatch = domain.OutputMatchMissing
		if tc.ProducedFile != "" {
			result.OutputSource = "file"
			result.ProducedFile = tc.ProducedFile
		} else {
			result.OutputSource = "stdout"
		}
		return result
	}

	if tc.ProducedFile != "" {
		result.OutputSource = "file"
		result.ProducedFile = tc.ProducedFile
		producedPath := filepath.Join(e.GetSubmissionBinaryDir(sub), tc.ProducedFile)
		producedData, readErr := os.ReadFile(producedPath)
		if readErr != nil {
			result.OutputMatch = domain.OutputMatchMissing
			return result
		}
		result.OutputMatch, result.OutputDiff = domain.ComputeOutputDiff(string(expectedData), string(producedData))
	} else {
		result.OutputSource = "stdout"
		result.OutputMatch, result.OutputDiff = domain.ComputeOutputDiff(string(expectedData), result.Stdout)
	}

	return result
}

func (e *Executor) HasMultiProcess() bool {
	return e.policy.Run.MultiProcess != nil && e.policy.Run.MultiProcess.Enabled
}

func (e *Executor) executionCommand(ctx context.Context, binaryDir, binaryPath string, args []string) (*exec.Cmd, string, func(), *domain.ValgrindResult) {
	valgrindPath, err := exec.LookPath("valgrind")
	if err != nil {
		return nil, "", func() {}, domain.NewValgrindMissingResult("valgrind")
	}

	logFile, err := os.CreateTemp(binaryDir, "valgrind-*.log")
	if err != nil {
		return nil, "", func() {}, domain.NewValgrindFailureResult(fmt.Sprintf("Could not create Valgrind log file: %v", err))
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

	cmdline := append([]string{valgrindPath}, valgrindArgs...)
	cleanup := func() {}
	if sandboxAvailable() {
		spec := sandboxSpec{workDir: binaryDir, readOnly: existingPaths(e.testFilesDir)}
		cmdline, cleanup = sandboxCommand(spec, cmdline)
	}

	return exec.CommandContext(ctx, cmdline[0], cmdline[1:]...), logPath, cleanup, nil
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

func (e *Executor) ExecuteMultiProcess(ctx context.Context, sub domain.Submission) *domain.MultiProcessResult {
	return e.executeMultiProcessWithOverrides(ctx, sub, nil)
}

func (e *Executor) ExecuteMultiProcessScenario(ctx context.Context, sub domain.Submission, scenario policy.MultiProcessScenario) *domain.MultiProcessResult {
	return e.executeMultiProcessWithOverrides(ctx, sub, &scenario)
}

// executeMultiProcessWithOverrides runs each configured executable as its own
// process. Processes run concurrently, with stdout/stderr buffered in memory.
// The function blocks until every process finishes (or ctx is cancelled) and
// returns a single MultiProcessResult — there is no live progress callback.
func (e *Executor) executeMultiProcessWithOverrides(ctx context.Context, sub domain.Submission, scenario *policy.MultiProcessScenario) *domain.MultiProcessResult {
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

	for _, proc := range config.Executables {
		var args []string
		var input string
		delayMs := 0

		if scenario != nil {
			args = scenario.ProcessArgs[proc.Name()]
			input = scenario.ProcessInputs[proc.Name()]
			delayMs = scenario.ProcessDelays[proc.Name()]
		}

		args = e.resolveTestFilePaths(args)

		procResult := &domain.ProcessResult{
			Name:       proc.Name(),
			SourceFile: proc.SourceFile,
		}
		result.AddProcess(proc.Name(), procResult)

		wg.Add(1)
		go func(proc policy.ProcessConfig, args []string, input string, delayMs int, procResult *domain.ProcessResult) {
			defer wg.Done()
			e.runOneProcess(ctx, sub, proc, args, input, delayMs, scenario, procResult)
		}(proc, args, input, delayMs, procResult)
	}

	wg.Wait()

	result.TotalDuration = time.Since(start)
	computeMultiProcessStatus(result)
	return result
}

// runOneProcess executes a single process from a multi-process config, buffering
// its stdout/stderr and recording the outcome on procResult.
func (e *Executor) runOneProcess(ctx context.Context, sub domain.Submission, proc policy.ProcessConfig, args []string, input string, delayMs int, scenario *policy.MultiProcessScenario, procResult *domain.ProcessResult) {
	if delayMs > 0 {
		select {
		case <-time.After(time.Duration(delayMs) * time.Millisecond):
		case <-ctx.Done():
			procResult.Killed = true
			return
		}
	}

	procResult.StartedAt = time.Now()
	defer func() {
		procResult.FinishedAt = time.Now()
		procResult.Duration = procResult.FinishedAt.Sub(procResult.StartedAt)
	}()

	binaryDir := e.GetSubmissionBinaryDir(sub)
	binaryPath := filepath.Join(binaryDir, strings.TrimSuffix(proc.SourceFile, ".c"))

	if _, err := os.Stat(binaryPath); err != nil {
		procResult.Stderr = fmt.Sprintf("Binary not found: %s (compilation may have failed)", binaryPath)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, DefaultExecTimeout)
	defer cancel()

	cmd, valgrindLogPath, cleanup, preflightFailure := e.executionCommand(ctx, binaryDir, binaryPath, args)
	defer cleanup()
	if preflightFailure != nil {
		procResult.Stderr = preflightFailure.Message
		procResult.Valgrind = preflightFailure
		return
	}
	configureProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.Dir = binaryDir
	cmd.Env = MinimalEnv(binaryDir)
	if input != "" {
		cmd.Stdin = strings.NewReader(unescapeInput(input))
	}

	stdout := &cappedBuffer{limit: maxCapturedOutput}
	stderr := &cappedBuffer{limit: maxCapturedOutput}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()

	procResult.Stdout = stdout.String()
	procResult.Stderr = stderr.String()

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

	procResult.CrashReason = crashReasonFromExit(err, procResult.TimedOut)
	procResult.Passed = runOK && !procResult.Killed && procResult.CrashReason == ""

	if scenario != nil && scenario.ExpectedOutputs != nil {
		if expectedFile, ok := scenario.ExpectedOutputs[proc.Name()]; ok && expectedFile != "" {
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
}

func computeMultiProcessStatus(result *domain.MultiProcessResult) {
	allPassed := true
	for _, pr := range result.Processes {
		if pr.Killed || !pr.Passed {
			allPassed = false
		}
	}
	result.AllCompleted = true
	result.AllPassed = allPassed
}
