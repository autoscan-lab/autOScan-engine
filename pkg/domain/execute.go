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
	Type    string `json:"type"` // "same", "added", "removed"
	Content string `json:"content"`
	LineNum int    `json:"line_num,omitempty"`
}

type ExecuteResult struct {
	OK           bool
	Stdout       string
	Stderr       string
	Duration     time.Duration
	TimedOut     bool
	Args         []string
	Input        string
	TestCaseName string
	Passed       bool
	OutputMatch  OutputMatchStatus
	OutputDiff   []DiffLine
	Valgrind     *ValgrindResult
}

type TestSummary struct {
	SubmissionID          string           `json:"submission_id,omitempty"`
	Total                 int              `json:"total"`
	Passed                int              `json:"passed"`
	Failed                int              `json:"failed"`
	CompileFailed         int              `json:"compile_failed"`
	MissingExpectedOutput int              `json:"missing_expected_output"`
	Cases                 []TestCaseResult `json:"cases"`
}

type TestCaseResult struct {
	SubmissionID   string          `json:"submission_id,omitempty"`
	Index          int             `json:"index"`
	Name           string          `json:"name"`
	Status         string          `json:"status"`
	DurationMs     int64           `json:"duration_ms"`
	Message        string          `json:"message,omitempty"`
	OutputMatch    string          `json:"output_match,omitempty"`
	Stdout         string          `json:"stdout,omitempty"`
	Stderr         string          `json:"stderr,omitempty"`
	ExpectedOutput *string         `json:"expected_output,omitempty"`
	ActualOutput   *string         `json:"actual_output,omitempty"`
	DiffLines      []DiffLine      `json:"diff_lines,omitempty"`
	Valgrind       *ValgrindResult `json:"valgrind,omitempty"`
}

func NewExecuteResult(ok bool, stdout, stderr string, duration time.Duration, timedOut bool, args []string, input string) ExecuteResult {
	return ExecuteResult{
		OK: ok, Stdout: stdout, Stderr: stderr, Duration: duration,
		TimedOut: timedOut, Args: args, Input: input,
		Passed: ok && !timedOut,
	}
}

func EmptyTestSummary() TestSummary {
	return TestSummary{Cases: []TestCaseResult{}}
}

func NewTestSummary(total int) TestSummary {
	return TestSummary{Total: total, Cases: make([]TestCaseResult, 0, total)}
}

func (s *TestSummary) AddCase(result TestCaseResult) {
	s.Cases = append(s.Cases, result)

	switch result.Status {
	case "pass":
		s.Passed++
	case "compile_failed":
		s.CompileFailed++
	default:
		s.Failed++
	}

	if result.OutputMatch == string(OutputMatchMissing) {
		s.MissingExpectedOutput++
	}
}

func NewCompileFailedTestCaseResult(submissionID string, index int, name string) TestCaseResult {
	return TestCaseResult{
		SubmissionID: submissionID,
		Index:        index,
		Name:         name,
		Status:       "compile_failed",
		Message:      "Compilation failed; test was not executed.",
	}
}

func (r ExecuteResult) TestCaseResult(submissionID string, index int, expectedOutput *string) TestCaseResult {
	status := "pass"
	if r.TimedOut {
		status = "timeout"
	} else if !r.Passed {
		status = "fail"
	} else if r.OutputMatch == OutputMatchFail {
		status = "fail"
	} else if r.Valgrind != nil && r.Valgrind.Fails() {
		status = "fail"
	}

	actualOutput := r.Stdout
	result := TestCaseResult{
		SubmissionID:   submissionID,
		Index:          index,
		Name:           r.TestCaseName,
		Status:         status,
		DurationMs:     r.Duration.Milliseconds(),
		Stdout:         r.Stdout,
		Stderr:         r.Stderr,
		OutputMatch:    string(r.OutputMatch),
		ExpectedOutput: expectedOutput,
		ActualOutput:   &actualOutput,
		DiffLines:      r.OutputDiff,
		Valgrind:       r.Valgrind,
	}

	if r.OutputMatch == OutputMatchMissing {
		result.Message = "Expected output file was not found."
	} else if r.Valgrind != nil && r.Valgrind.Status == ValgrindStatusMissing {
		result.Message = r.Valgrind.Message
	} else if r.Valgrind != nil && r.Valgrind.Fails() {
		result.Message = "Valgrind reported memory or file descriptor issues."
	}

	return result
}

func (r ExecuteResult) WithTestCase(name string) ExecuteResult {
	r.TestCaseName = name
	return r
}

func (r ExecuteResult) WithValgrind(valgrind *ValgrindResult) ExecuteResult {
	r.Valgrind = valgrind
	if valgrind != nil && valgrind.Fails() {
		r.Passed = false
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
	Name        string
	SourceFile  string
	Stdout      string
	Stderr      string
	Duration    time.Duration
	TimedOut    bool
	Killed      bool
	Running     bool
	StartedAt   time.Time
	FinishedAt  time.Time
	Passed      bool
	OutputMatch OutputMatchStatus
	OutputDiff  []DiffLine
	Valgrind    *ValgrindResult
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
