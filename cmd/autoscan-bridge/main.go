package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	internalengine "github.com/autoscan-lab/autoscan-engine/internal/engine"
	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/engine"
	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

var version = "dev"

type bridgeEvent struct {
	Type            string                   `json:"type"`
	Message         string                   `json:"message,omitempty"`
	Version         string                   `json:"version,omitempty"`
	Discovery       *discoveryPayload        `json:"discovery,omitempty"`
	Compile         *compilePayload          `json:"compile,omitempty"`
	Scan            *scanPayload             `json:"scan,omitempty"`
	Run             *runCompletePayload      `json:"run,omitempty"`
	TestCaseStarted *testCaseStartedPayload  `json:"test_case_started,omitempty"`
	TestCase        *testCaseCompletePayload `json:"test_case,omitempty"`
	TestsComplete   *testsCompletePayload    `json:"tests_complete,omitempty"`
}

type discoveryPayload struct {
	SubmissionCount int                 `json:"submission_count"`
	Submissions     []submissionPayload `json:"submissions"`
}

type compilePayload struct {
	SubmissionID string `json:"submission_id"`
	OK           bool   `json:"ok"`
	ExitCode     int    `json:"exit_code"`
	TimedOut     bool   `json:"timed_out"`
	DurationMs   int64  `json:"duration_ms"`
	Stdout       string `json:"stdout,omitempty"`
	Stderr       string `json:"stderr,omitempty"`
}

type scanPayload struct {
	SubmissionID string   `json:"submission_id"`
	BannedHits   int      `json:"banned_hits"`
	ParseErrors  []string `json:"parse_errors,omitempty"`
}

type runCompletePayload struct {
	Summary     runSummaryPayload     `json:"summary"`
	Submissions []runSubmissionResult `json:"submissions"`
}

type runSummaryPayload struct {
	PolicyName            string         `json:"policy_name"`
	Root                  string         `json:"root"`
	StartedAt             string         `json:"started_at"`
	FinishedAt            string         `json:"finished_at"`
	DurationMs            int64          `json:"duration_ms"`
	TotalSubmissions      int            `json:"total_submissions"`
	CompilePass           int            `json:"compile_pass"`
	CompileFail           int            `json:"compile_fail"`
	CompileTimeout        int            `json:"compile_timeout"`
	CleanSubmissions      int            `json:"clean_submissions"`
	SubmissionsWithBanned int            `json:"submissions_with_banned"`
	BannedHitsTotal       int            `json:"banned_hits_total"`
	TopBannedFunctions    map[string]int `json:"top_banned_functions"`
}

type runSubmissionResult struct {
	ID             string             `json:"id"`
	Path           string             `json:"path"`
	Status         string             `json:"status"`
	CFiles         []string           `json:"c_files"`
	CompileOK      bool               `json:"compile_ok"`
	CompileTimeout bool               `json:"compile_timeout"`
	ExitCode       int                `json:"exit_code"`
	CompileTimeMs  int64              `json:"compile_time_ms"`
	Stderr         string             `json:"stderr,omitempty"`
	BannedCount    int                `json:"banned_count"`
	BannedHits     []bannedHitPayload `json:"banned_hits,omitempty"`
}

type submissionPayload struct {
	ID     string   `json:"id"`
	Path   string   `json:"path"`
	CFiles []string `json:"c_files"`
}

type capabilitiesPayload struct {
	RunSession  bool `json:"run_session"`
	RunTestCase bool `json:"run_test_case"`
	DiffPayload bool `json:"diff_payload"`
}

type bannedHitPayload struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Snippet  string `json:"snippet,omitempty"`
}

type testCaseStartedPayload struct {
	SubmissionID  string `json:"submission_id"`
	TestCaseIndex int    `json:"test_case_index"`
	TestCaseName  string `json:"test_case_name"`
}

type diffLinePayload struct {
	Type    string `json:"type"`
	Content string `json:"content"`
	LineNum int    `json:"line_num,omitempty"`
}

type testCaseCompletePayload struct {
	SubmissionID   string            `json:"submission_id"`
	TestCaseIndex  int               `json:"test_case_index"`
	TestCaseName   string            `json:"test_case_name"`
	Status         string            `json:"status"`
	ExitCode       int               `json:"exit_code"`
	DurationMs     int64             `json:"duration_ms"`
	Stdout         string            `json:"stdout,omitempty"`
	Stderr         string            `json:"stderr,omitempty"`
	OutputMatch    string            `json:"output_match,omitempty"`
	ExpectedOutput *string           `json:"expected_output,omitempty"`
	ActualOutput   *string           `json:"actual_output,omitempty"`
	DiffLines      []diffLinePayload `json:"diff_lines,omitempty"`
	Message        string            `json:"message,omitempty"`
}

type testsCompletePayload struct {
	SubmissionID        string `json:"submission_id"`
	Total               int    `json:"total"`
	Passed              int    `json:"passed"`
	Failed              int    `json:"failed"`
	CompileFailed       int    `json:"compile_failed"`
	MissingExpectedFile int    `json:"missing_expected_output"`
}

type eventWriter struct {
	mu      sync.Mutex
	encoder *json.Encoder
}

func newEventWriter() *eventWriter {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return &eventWriter{encoder: encoder}
}

func (w *eventWriter) emit(event bridgeEvent) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.encoder.Encode(event)
}

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	writer := newEventWriter()

	if len(os.Args) < 2 {
		return emitCommandError(
			writer,
			errors.New("usage: autoscan-bridge <version|capabilities|run-session|run-test-case>"),
		)
	}

	switch os.Args[1] {
	case "version":
		return writer.emit(bridgeEvent{
			Type:    "version",
			Version: version,
			Message: "autoscan-bridge ready",
		})
	case "capabilities":
		return emitCapabilities()
	case "run-session":
		return runCommand(writer, os.Args[2:])
	case "run-test-case":
		return runTestCaseCommand(writer, os.Args[2:])
	default:
		return emitCommandError(writer, fmt.Errorf("unknown command %q", os.Args[1]))
	}
}

func emitCommandError(writer *eventWriter, err error) error {
	if err == nil {
		return nil
	}
	if writer != nil {
		_ = writer.emit(bridgeEvent{
			Type:    "error",
			Message: err.Error(),
		})
	}
	return err
}

func emitCapabilities() error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(capabilitiesPayload{
		RunSession:  true,
		RunTestCase: true,
		DiffPayload: true,
	})
}

func runCommand(writer *eventWriter, args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	workspacePath := flags.String("workspace", "", "Path to the workspace or submission root")
	policyPath := flags.String("policy", "", "Path to the policy file")
	configDir := flags.String("config-dir", "", "Config directory override")
	outputDir := flags.String("output-dir", "", "Binary output directory")
	workers := flags.Int("workers", 0, "Worker count override")
	shortNames := flags.Bool("short-names", false, "Use short submission directory names")

	if err := flags.Parse(args); err != nil {
		return emitCommandError(writer, err)
	}

	if *workspacePath == "" {
		return emitCommandError(writer, errors.New("missing required --workspace"))
	}
	if *policyPath == "" {
		return emitCommandError(writer, errors.New("missing required --policy"))
	}

	if *configDir != "" {
		if err := os.Setenv("AUTOSCAN_CONFIG_DIR", *configDir); err != nil {
			return fmt.Errorf("setting AUTOSCAN_CONFIG_DIR: %w", err)
		}
	}

	loadedPolicy, err := policy.LoadWithGlobals(*policyPath)
	if err != nil {
		return fmt.Errorf("loading policy: %w", err)
	}

	rootPath, err := filepath.Abs(*workspacePath)
	if err != nil {
		return fmt.Errorf("resolving workspace path: %w", err)
	}

	opts := []engine.CompileOption{
		engine.WithShortNames(*shortNames),
	}
	if *workers > 0 {
		opts = append(opts, engine.WithWorkers(*workers))
	}
	if *outputDir != "" {
		opts = append(opts, engine.WithOutputDir(*outputDir))
	}

	runner, err := engine.NewRunner(loadedPolicy, opts...)
	if err != nil {
		return fmt.Errorf("creating runner: %w", err)
	}
	if *outputDir == "" {
		defer runner.Cleanup()
	}

	if err := writer.emit(bridgeEvent{
		Type:    "started",
		Message: fmt.Sprintf("Starting workspace run for %s with %s.", rootPath, loadedPolicy.Name),
	}); err != nil {
		return err
	}

	report, err := runner.Run(context.Background(), rootPath, engine.RunnerCallbacks{
		OnDiscoveryComplete: func(submissions []domain.Submission) {
			payload := discoveryPayload{
				SubmissionCount: len(submissions),
				Submissions:     make([]submissionPayload, len(submissions)),
			}
			for index, submission := range submissions {
				payload.Submissions[index] = submissionPayload{
					ID:     submission.ID,
					Path:   submission.Path,
					CFiles: submission.CFiles,
				}
			}
			_ = writer.emit(bridgeEvent{
				Type:      "discovery_complete",
				Discovery: &payload,
			})
		},
		OnCompileComplete: func(sub domain.Submission, result domain.CompileResult) {
			_ = writer.emit(bridgeEvent{
				Type: "compile_complete",
				Compile: &compilePayload{
					SubmissionID: sub.ID,
					OK:           result.OK,
					ExitCode:     result.ExitCode,
					TimedOut:     result.TimedOut,
					DurationMs:   result.DurationMs,
					Stdout:       result.Stdout,
					Stderr:       result.Stderr,
				},
			})
		},
		OnScanComplete: func(sub domain.Submission, result domain.ScanResult) {
			_ = writer.emit(bridgeEvent{
				Type: "scan_complete",
				Scan: &scanPayload{
					SubmissionID: sub.ID,
					BannedHits:   result.TotalHits(),
					ParseErrors:  result.ParseErrors,
				},
			})
		},
		OnAllComplete: func(report domain.RunReport) {
			_ = writer.emit(bridgeEvent{
				Type: "run_complete",
				Run:  buildRunCompletePayload(report),
			})
		},
	})
	if err != nil {
		_ = writer.emit(bridgeEvent{
			Type:    "error",
			Message: err.Error(),
		})
		return fmt.Errorf("running engine: %w", err)
	}

	if report == nil {
		return errors.New("engine finished without a report")
	}

	return nil
}

func runTestCaseCommand(writer *eventWriter, args []string) error {
	flags := flag.NewFlagSet("run-tests", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	workspacePath := flags.String("workspace", "", "Path to the workspace root")
	policyPath := flags.String("policy", "", "Path to the policy file")
	submissionID := flags.String("submission-id", "", "Submission ID from discovery")
	testCaseIndex := flags.Int("test-case-index", -1, "Test case index (0-based)")
	configDir := flags.String("config-dir", "", "Config directory override")
	outputDir := flags.String("output-dir", "", "Binary output directory")
	workers := flags.Int("workers", 0, "Worker count override")
	shortNames := flags.Bool("short-names", false, "Use short submission directory names")

	if err := flags.Parse(args); err != nil {
		return emitCommandError(writer, err)
	}

	if *workspacePath == "" {
		return emitCommandError(writer, errors.New("missing required --workspace"))
	}
	if *policyPath == "" {
		return emitCommandError(writer, errors.New("missing required --policy"))
	}
	if *submissionID == "" {
		return emitCommandError(writer, errors.New("missing required --submission-id"))
	}
	if *testCaseIndex < 0 {
		return emitCommandError(writer, errors.New("missing required --test-case-index"))
	}

	if *configDir != "" {
		if err := os.Setenv("AUTOSCAN_CONFIG_DIR", *configDir); err != nil {
			return fmt.Errorf("setting AUTOSCAN_CONFIG_DIR: %w", err)
		}
	}

	loadedPolicy, err := policy.LoadWithGlobals(*policyPath)
	if err != nil {
		return fmt.Errorf("loading policy: %w", err)
	}

	rootPath, err := filepath.Abs(*workspacePath)
	if err != nil {
		return fmt.Errorf("resolving workspace path: %w", err)
	}

	if err := writer.emit(bridgeEvent{
		Type:    "started",
		Message: fmt.Sprintf("Starting test case #%d for submission %s.", *testCaseIndex, *submissionID),
	}); err != nil {
		return err
	}

	discovery := internalengine.NewDiscoveryEngine(loadedPolicy)
	submissions, err := discovery.Discover(rootPath)
	if err != nil {
		_ = writer.emit(bridgeEvent{Type: "error", Message: err.Error()})
		return fmt.Errorf("discovering submissions: %w", err)
	}

	discoveryData := discoveryPayload{
		SubmissionCount: len(submissions),
		Submissions:     make([]submissionPayload, len(submissions)),
	}
	for index, submission := range submissions {
		discoveryData.Submissions[index] = submissionPayload{
			ID:     submission.ID,
			Path:   submission.Path,
			CFiles: submission.CFiles,
		}
	}
	_ = writer.emit(bridgeEvent{Type: "discovery_complete", Discovery: &discoveryData})

	target, found := findSubmissionByID(submissions, *submissionID)
	if !found {
		msg := fmt.Sprintf("submission not found: %s", *submissionID)
		_ = writer.emit(bridgeEvent{Type: "error", Message: msg})
		return errors.New(msg)
	}

	activeOutputDir := *outputDir
	cleanupOutputDir := ""
	if activeOutputDir == "" {
		tempDir, err := os.MkdirTemp("", "autoscan-tests-*")
		if err != nil {
			_ = writer.emit(bridgeEvent{Type: "error", Message: err.Error()})
			return fmt.Errorf("creating temporary output directory: %w", err)
		}
		activeOutputDir = tempDir
		cleanupOutputDir = tempDir
	}
	if cleanupOutputDir != "" {
		defer os.RemoveAll(cleanupOutputDir)
	}

	compileOpts := []internalengine.CompileOption{
		internalengine.WithShortNames(*shortNames),
		internalengine.WithOutputDir(activeOutputDir),
	}
	if *workers > 0 {
		compileOpts = append(compileOpts, internalengine.WithWorkers(*workers))
	}

	compiler, err := internalengine.NewCompileEngine(loadedPolicy, compileOpts...)
	if err != nil {
		_ = writer.emit(bridgeEvent{Type: "error", Message: err.Error()})
		return fmt.Errorf("creating compile engine: %w", err)
	}
	defer compiler.Cleanup()

	compiled := compiler.CompileAll(context.Background(), []domain.Submission{target}, nil)
	compileResult := compiled[0]
	_ = writer.emit(bridgeEvent{
		Type: "compile_complete",
		Compile: &compilePayload{
			SubmissionID: target.ID,
			OK:           compileResult.OK,
			ExitCode:     compileResult.ExitCode,
			TimedOut:     compileResult.TimedOut,
			DurationMs:   compileResult.DurationMs,
			Stdout:       compileResult.Stdout,
			Stderr:       compileResult.Stderr,
		},
	})

	testCases := loadedPolicy.Run.TestCases
	if len(testCases) == 0 {
		_ = writer.emit(bridgeEvent{
			Type: "tests_complete",
			TestsComplete: &testsCompletePayload{
				SubmissionID:        target.ID,
				Total:               0,
				Passed:              0,
				Failed:              0,
				CompileFailed:       0,
				MissingExpectedFile: 0,
			},
		})
		return nil
	}

	if *testCaseIndex >= len(testCases) {
		msg := fmt.Sprintf("invalid --test-case-index: %d (have %d test cases)", *testCaseIndex, len(testCases))
		_ = writer.emit(bridgeEvent{Type: "error", Message: msg})
		return errors.New(msg)
	}

	summary := testsCompletePayload{SubmissionID: target.ID, Total: 1}
	tc := testCases[*testCaseIndex]

	if compileResult.TimedOut || !compileResult.OK {
		summary.CompileFailed = 1
		_ = writer.emit(bridgeEvent{
			Type: "test_case_complete",
			TestCase: &testCaseCompletePayload{
				SubmissionID:  target.ID,
				TestCaseIndex: *testCaseIndex,
				TestCaseName:  tc.Name,
				Status:        "compile_failed",
				ExitCode:      compileResult.ExitCode,
				DurationMs:    0,
				Message:       "Compilation failed; test was not executed.",
			},
		})
		_ = writer.emit(bridgeEvent{Type: "tests_complete", TestsComplete: &summary})
		return nil
	}

	executor := internalengine.NewExecutorWithOptions(loadedPolicy, activeOutputDir, *shortNames)

	_ = writer.emit(bridgeEvent{
		Type: "test_case_started",
		TestCaseStarted: &testCaseStartedPayload{
			SubmissionID:  target.ID,
			TestCaseIndex: *testCaseIndex,
			TestCaseName:  tc.Name,
		},
	})

	result := executor.ExecuteTestCase(context.Background(), target, tc)
	payload, missingExpected := buildTestCaseCompletePayload(target, *testCaseIndex, tc, result)
	if missingExpected {
		summary.MissingExpectedFile++
	}

	if payload.Status == "pass" {
		summary.Passed = 1
	} else if payload.Status == "compile_failed" {
		summary.CompileFailed = 1
	} else {
		summary.Failed = 1
	}

	_ = writer.emit(bridgeEvent{Type: "test_case_complete", TestCase: payload})
	_ = writer.emit(bridgeEvent{Type: "tests_complete", TestsComplete: &summary})
	return nil
}

func buildTestCaseCompletePayload(
	submission domain.Submission,
	index int,
	tc policy.TestCase,
	result domain.ExecuteResult,
) (*testCaseCompletePayload, bool) {
	status := "pass"
	if result.TimedOut {
		status = "timeout"
	} else if !result.Passed {
		status = "fail"
	}

	actualOutput := result.Stdout
	payload := &testCaseCompletePayload{
		SubmissionID:  submission.ID,
		TestCaseIndex: index,
		TestCaseName:  tc.Name,
		Status:        status,
		ExitCode:      result.ExitCode,
		DurationMs:    result.Duration.Milliseconds(),
		Stdout:        result.Stdout,
		Stderr:        result.Stderr,
		OutputMatch:   string(result.OutputMatch),
		ActualOutput:  &actualOutput,
	}

	expectedOutput, expectedAvailable := loadExpectedOutput(tc)
	if expectedAvailable {
		payload.ExpectedOutput = &expectedOutput
	}

	if len(result.OutputDiff) > 0 {
		payload.DiffLines = make([]diffLinePayload, len(result.OutputDiff))
		for i, line := range result.OutputDiff {
			payload.DiffLines[i] = diffLinePayload{
				Type:    line.Type,
				Content: line.Content,
				LineNum: line.LineNum,
			}
		}
	}

	if result.OutputMatch == domain.OutputMatchMissing {
		payload.Message = "Expected output file was not found."
	}

	return payload, result.OutputMatch == domain.OutputMatchMissing
}

func loadExpectedOutput(tc policy.TestCase) (string, bool) {
	if tc.ExpectedOutputFile == "" {
		return "", false
	}

	configDir, err := policy.ConfigDir()
	if err != nil {
		return "", false
	}

	expectedPath := filepath.Join(configDir, "expected_outputs", tc.ExpectedOutputFile)
	data, err := os.ReadFile(expectedPath)
	if err != nil {
		return "", false
	}

	return string(data), true
}

func findSubmissionByID(submissions []domain.Submission, submissionID string) (domain.Submission, bool) {
	for _, submission := range submissions {
		if submission.ID == submissionID {
			return submission, true
		}
	}
	return domain.Submission{}, false
}

func buildRunCompletePayload(report domain.RunReport) *runCompletePayload {
	payload := &runCompletePayload{
		Summary: runSummaryPayload{
			PolicyName:            report.PolicyName,
			Root:                  report.Root,
			StartedAt:             report.StartedAt.Format("2006-01-02T15:04:05Z07:00"),
			FinishedAt:            report.FinishedAt.Format("2006-01-02T15:04:05Z07:00"),
			DurationMs:            report.Summary.DurationMs,
			TotalSubmissions:      report.Summary.TotalSubmissions,
			CompilePass:           report.Summary.CompilePass,
			CompileFail:           report.Summary.CompileFail,
			CompileTimeout:        report.Summary.CompileTimeout,
			CleanSubmissions:      report.Summary.CleanSubmissions,
			SubmissionsWithBanned: report.Summary.SubmissionsWithBanned,
			BannedHitsTotal:       report.Summary.BannedHitsTotal,
			TopBannedFunctions:    report.Summary.TopBannedFunctions,
		},
		Submissions: make([]runSubmissionResult, len(report.Results)),
	}

	for index, result := range report.Results {
		payload.Submissions[index] = runSubmissionResult{
			ID:             result.Submission.ID,
			Path:           result.Submission.Path,
			Status:         string(result.Status),
			CFiles:         result.Submission.CFiles,
			CompileOK:      result.Compile.OK,
			CompileTimeout: result.Compile.TimedOut,
			ExitCode:       result.Compile.ExitCode,
			CompileTimeMs:  result.Compile.DurationMs,
			Stderr:         result.Compile.Stderr,
			BannedCount:    result.Scan.TotalHits(),
			BannedHits:     make([]bannedHitPayload, len(result.Scan.Hits)),
		}

		for hitIndex, hit := range result.Scan.Hits {
			payload.Submissions[index].BannedHits[hitIndex] = bannedHitPayload{
				Function: hit.Function,
				File:     hit.File,
				Line:     hit.Line,
				Column:   hit.Column,
				Snippet:  hit.Snippet,
			}
		}
	}

	return payload
}
