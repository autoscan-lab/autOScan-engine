package domain

import (
	"strings"
	"time"
)

type OutputMatchStatus string

const (
	OutputMatchNone    OutputMatchStatus = "none"    // No expected output file configured
	OutputMatchPass    OutputMatchStatus = "pass"    // Output matches expected
	OutputMatchFail    OutputMatchStatus = "fail"    // Output differs from expected
	OutputMatchMissing OutputMatchStatus = "missing" // Expected output file not found
)

type DiffLine struct {
	Type    string // "same", "added", "removed"
	Content string
	LineNum int
}

type ExecuteResult struct {
	OK           bool
	ExitCode     int
	Stdout       string
	Stderr       string
	Duration     time.Duration
	TimedOut     bool
	Args         []string
	Input        string
	TestCaseName string
	ExpectedExit *int
	Passed       bool
	OutputMatch  OutputMatchStatus
	OutputDiff   []DiffLine
}

func NewExecuteResult(ok bool, exitCode int, stdout, stderr string, duration time.Duration, timedOut bool, args []string, input string) ExecuteResult {
	return ExecuteResult{
		OK: ok, ExitCode: exitCode, Stdout: stdout, Stderr: stderr,
		Duration: duration, TimedOut: timedOut, Args: args, Input: input,
		Passed: ok && !timedOut,
	}
}

func (r ExecuteResult) WithTestCase(name string, expectedExit *int) ExecuteResult {
	r.TestCaseName = name
	r.ExpectedExit = expectedExit
	if expectedExit != nil {
		r.Passed = r.OK && !r.TimedOut && r.ExitCode == *expectedExit
	}
	return r
}

type MultiProcessResult struct {
	Processes     map[string]*ProcessResult
	Order         []string
	TotalDuration time.Duration
	AllCompleted  bool
	AllPassed     bool
	ScenarioName  string
}

type ProcessResult struct {
	Name         string
	SourceFile   string
	ExitCode     int
	Stdout       string
	Stderr       string
	Duration     time.Duration
	TimedOut     bool
	Killed       bool
	Running      bool
	StartedAt    time.Time
	FinishedAt   time.Time
	ExpectedExit *int
	Passed       bool
	OutputMatch  OutputMatchStatus
	OutputDiff   []DiffLine
}

func NewMultiProcessResult() *MultiProcessResult {
	return &MultiProcessResult{Processes: make(map[string]*ProcessResult), Order: []string{}}
}

func (m *MultiProcessResult) AddProcess(name string, result *ProcessResult) {
	if _, exists := m.Processes[name]; !exists {
		m.Order = append(m.Order, name)
	}
	m.Processes[name] = result
}

// Computes output diff between expected and actual output
func ComputeOutputDiff(expected, actual string) (OutputMatchStatus, []DiffLine) {
	expected = strings.ReplaceAll(expected, "\r\n", "\n")
	actual = strings.ReplaceAll(actual, "\r\n", "\n")

	expected = strings.TrimSuffix(expected, "\n")
	actual = strings.TrimSuffix(actual, "\n")

	if expected == actual {
		return OutputMatchPass, nil
	}

	expectedLines := strings.Split(expected, "\n")
	actualLines := strings.Split(actual, "\n")

	var diff []DiffLine
	maxLen := len(expectedLines)
	if len(actualLines) > maxLen {
		maxLen = len(actualLines)
	}

	for i := 0; i < maxLen; i++ {
		var exp, act string
		if i < len(expectedLines) {
			exp = expectedLines[i]
		}
		if i < len(actualLines) {
			act = actualLines[i]
		}

		if exp == act {
			diff = append(diff, DiffLine{Type: "same", Content: act, LineNum: i + 1})
		} else {
			if i < len(expectedLines) {
				diff = append(diff, DiffLine{Type: "removed", Content: exp, LineNum: i + 1})
			}
			if i < len(actualLines) {
				diff = append(diff, DiffLine{Type: "added", Content: act, LineNum: i + 1})
			}
		}
	}

	return OutputMatchFail, diff
}
