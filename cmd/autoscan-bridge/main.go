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
	Type            string                  `json:"type"`
	Message         string                  `json:"message,omitempty"`
	Version         string                  `json:"version,omitempty"`
	Discovery       *discoveryPayload       `json:"discovery,omitempty"`
	Compile         *compileEventPayload    `json:"compile,omitempty"`
	Scan            *scanEventPayload       `json:"scan,omitempty"`
	Run             *domain.RunReport       `json:"run,omitempty"`
	TestCaseStarted *testCaseStartedPayload `json:"test_case_started,omitempty"`
	TestCase        *domain.TestCaseResult  `json:"test_case,omitempty"`
	TestsComplete   *domain.TestSummary     `json:"tests_complete,omitempty"`
}

type discoveryPayload struct {
	SubmissionCount int                 `json:"submission_count"`
	Submissions     []domain.Submission `json:"submissions"`
}

type compileEventPayload struct {
	SubmissionID string `json:"submission_id"`
	domain.CompileResult
}

type scanEventPayload struct {
	SubmissionID string `json:"submission_id"`
	domain.ScanResult
}

type capabilitiesPayload struct {
	RunSession  bool `json:"run_session"`
	RunTestCase bool `json:"run_test_case"`
	DiffPayload bool `json:"diff_payload"`
}

type testCaseStartedPayload struct {
	SubmissionID  string `json:"submission_id"`
	TestCaseIndex int    `json:"test_case_index"`
	TestCaseName  string `json:"test_case_name"`
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

	loadedPolicy, err := policy.LoadWithGlobalsFromConfigDir(*policyPath, *configDir)
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
				Submissions:     append([]domain.Submission(nil), submissions...),
			}
			_ = writer.emit(bridgeEvent{
				Type:      "discovery_complete",
				Discovery: &payload,
			})
		},
		OnCompileComplete: func(sub domain.Submission, result domain.CompileResult) {
			_ = writer.emit(bridgeEvent{
				Type: "compile_complete",
				Compile: &compileEventPayload{
					SubmissionID:  sub.ID,
					CompileResult: result,
				},
			})
		},
		OnScanComplete: func(sub domain.Submission, result domain.ScanResult) {
			_ = writer.emit(bridgeEvent{
				Type: "scan_complete",
				Scan: &scanEventPayload{
					SubmissionID: sub.ID,
					ScanResult:   result,
				},
			})
		},
		OnAllComplete: func(report domain.RunReport) {
			reportCopy := report
			_ = writer.emit(bridgeEvent{
				Type: "run_complete",
				Run:  &reportCopy,
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

	loadedPolicy, err := policy.LoadWithGlobalsFromConfigDir(*policyPath, *configDir)
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
		Submissions:     append([]domain.Submission(nil), submissions...),
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
		Compile: &compileEventPayload{
			SubmissionID:  target.ID,
			CompileResult: compileResult,
		},
	})

	testCases := loadedPolicy.Run.TestCases
	if len(testCases) == 0 {
		summary := domain.EmptyTestSummary()
		summary.SubmissionID = target.ID
		_ = writer.emit(bridgeEvent{
			Type:          "tests_complete",
			TestsComplete: &summary,
		})
		return nil
	}

	if *testCaseIndex >= len(testCases) {
		msg := fmt.Sprintf("invalid --test-case-index: %d (have %d test cases)", *testCaseIndex, len(testCases))
		_ = writer.emit(bridgeEvent{Type: "error", Message: msg})
		return errors.New(msg)
	}

	summary := domain.NewTestSummary(1)
	summary.SubmissionID = target.ID
	tc := testCases[*testCaseIndex]

	if compileResult.TimedOut || !compileResult.OK {
		summary.AddCase(domain.NewCompileFailedTestCaseResult(target.ID, *testCaseIndex, tc.Name, compileResult.ExitCode))
		_ = writer.emit(bridgeEvent{
			Type:     "test_case_complete",
			TestCase: &summary.Cases[0],
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
	payload := buildTestCaseCompletePayload(loadedPolicy, target, *testCaseIndex, tc, result)
	summary.AddCase(*payload)

	_ = writer.emit(bridgeEvent{Type: "test_case_complete", TestCase: payload})
	_ = writer.emit(bridgeEvent{Type: "tests_complete", TestsComplete: &summary})
	return nil
}

func buildTestCaseCompletePayload(
	loadedPolicy *policy.Policy,
	submission domain.Submission,
	index int,
	tc policy.TestCase,
	result domain.ExecuteResult,
) *domain.TestCaseResult {
	var expectedOutput *string
	output, expectedAvailable := loadExpectedOutput(loadedPolicy, tc)
	if expectedAvailable {
		expectedOutput = &output
	}

	payload := result.TestCaseResult(submission.ID, index, expectedOutput)
	return &payload
}

func loadExpectedOutput(loadedPolicy *policy.Policy, tc policy.TestCase) (string, bool) {
	if tc.ExpectedOutputFile == "" {
		return "", false
	}

	expectedPath := filepath.Join(loadedPolicy.EffectiveConfigDir(), "expected_outputs", tc.ExpectedOutputFile)
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
