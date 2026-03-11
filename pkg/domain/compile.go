package domain

type CompileResult struct {
	OK         bool
	Command    []string
	ExitCode   int
	Stdout     string
	Stderr     string
	DurationMs int64
	TimedOut   bool
}

func NewCompileResult(ok bool, command []string, exitCode int, stdout, stderr string, durationMs int64, timedOut bool) CompileResult {
	return CompileResult{
		OK: ok, Command: command, ExitCode: exitCode,
		Stdout: stdout, Stderr: stderr, DurationMs: durationMs, TimedOut: timedOut,
	}
}
