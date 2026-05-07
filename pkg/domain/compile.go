package domain

type CompileResult struct {
	OK         bool     `json:"ok"`
	Command    []string `json:"command,omitempty"`
	Stdout     string   `json:"stdout,omitempty"`
	Stderr     string   `json:"stderr,omitempty"`
	DurationMs int64    `json:"duration_ms"`
	TimedOut   bool     `json:"timed_out"`
}

func NewCompileResult(ok bool, command []string, stdout, stderr string, durationMs int64, timedOut bool) CompileResult {
	return CompileResult{
		OK: ok, Command: command, Stdout: stdout, Stderr: stderr,
		DurationMs: durationMs, TimedOut: timedOut,
	}
}
