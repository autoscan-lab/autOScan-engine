package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/engine"
	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

type gradeResponse struct {
	PolicyName  string                    `json:"policy_name"`
	Root        string                    `json:"root"`
	StartedAt   string                    `json:"started_at"`
	FinishedAt  string                    `json:"finished_at"`
	Summary     domain.SummaryStats       `json:"summary"`
	Results     []submissionResult        `json:"results"`
	Similarity  *domain.SimilarityReport  `json:"similarity,omitempty"`
	AIDetection *domain.AIDetectionReport `json:"ai_detection,omitempty"`
}

type gradeOptions struct {
	IncludeSimilarity  bool
	IncludeAIDetection bool
}

// submissionResult embeds the engine's SubmissionResult so the response
// mirrors the engine shape directly: submission, compile, scan, status —
// then we add server-only fields (source_files, tests).
type submissionResult struct {
	domain.SubmissionResult
	SourceFiles []sourceFile       `json:"source_files,omitempty"`
	Tests       domain.TestSummary `json:"tests"`
}

type sourceFile struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// runGradingPipeline drives discovery → compile → scan → run-all-test-cases
// against the workspace, producing a single canonical response. Compilation
// happens once per submission; every test case reuses the resulting binary.
func runGradingPipeline(ctx context.Context, cfg config, workspaceDir string, opts gradeOptions) (*gradeResponse, error) {
	policyPath := filepath.Join(cfg.currentDir, policyFileName)

	loadedPolicy, err := policy.LoadWithGlobalsFromConfigDir(policyPath, cfg.currentDir)
	if err != nil {
		return nil, fmt.Errorf("loading policy: %w", err)
	}

	rootPath, err := filepath.Abs(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("resolving workspace path: %w", err)
	}

	binaryDir, err := os.MkdirTemp("", "autoscan-bin-*")
	if err != nil {
		return nil, fmt.Errorf("creating binary output dir: %w", err)
	}
	defer os.RemoveAll(binaryDir)

	runner, err := engine.NewRunner(loadedPolicy, engine.WithOutputDir(binaryDir))
	if err != nil {
		return nil, fmt.Errorf("creating runner: %w", err)
	}
	defer runner.Cleanup()

	report, err := runner.Run(ctx, rootPath, engine.RunnerCallbacks{})
	if err != nil {
		return nil, fmt.Errorf("running engine: %w", err)
	}
	if report == nil {
		return nil, fmt.Errorf("engine finished without a report")
	}

	resp := buildBaseResponse(report)

	if len(loadedPolicy.Run.TestCases) > 0 {
		executor := engine.NewExecutorWithOptions(loadedPolicy, binaryDir, false)
		expected := loadExpectedOutputs(loadedPolicy)
		for i := range resp.Results {
			runTestCases(ctx, executor, loadedPolicy, report.Results[i], &resp.Results[i], expected)
		}
	}

	for i := range resp.Results {
		resp.Results[i].SourceFiles = readSourceFiles(report.Results[i].Submission)
	}

	sim, ai, err := runAnalysis(ctx, cfg, loadedPolicy, report, opts.IncludeSimilarity, opts.IncludeAIDetection)
	if err != nil {
		return nil, err
	}
	resp.Similarity = sim
	resp.AIDetection = ai

	return resp, nil
}

func buildBaseResponse(report *domain.RunReport) *gradeResponse {
	resp := &gradeResponse{
		PolicyName: report.PolicyName,
		Root:       report.Root,
		StartedAt:  report.StartedAt.Format("2006-01-02T15:04:05Z07:00"),
		FinishedAt: report.FinishedAt.Format("2006-01-02T15:04:05Z07:00"),
		Summary:    report.Summary,
		Results:    make([]submissionResult, len(report.Results)),
	}

	for i, r := range report.Results {
		resp.Results[i] = submissionResult{
			SubmissionResult: r,
			Tests:            domain.EmptyTestSummary(),
		}
	}

	return resp
}

func runTestCases(
	ctx context.Context,
	executor *engine.Executor,
	loadedPolicy *policy.Policy,
	subResult domain.SubmissionResult,
	out *submissionResult,
	expected map[string]*string,
) {
	cases := loadedPolicy.Run.TestCases
	out.Tests = domain.NewTestSummary(len(cases))

	if subResult.Compile.TimedOut || !subResult.Compile.OK {
		for i, tc := range cases {
			out.Tests.AddCase(domain.NewCompileFailedTestCaseResult("", i, tc.Name, subResult.Compile.ExitCode))
		}
		return
	}

	for i, tc := range cases {
		result := executor.ExecuteTestCase(ctx, subResult.Submission, tc)
		payload := buildTestCasePayload(i, tc, result, expected)
		out.Tests.AddCase(payload)
	}
}

func buildTestCasePayload(index int, tc policy.TestCase, result domain.ExecuteResult, expected map[string]*string) domain.TestCaseResult {
	var expectedOutput *string
	if exp, ok := expected[tc.ExpectedOutputFile]; ok && exp != nil {
		expectedOutput = exp
	}
	return result.TestCaseResult("", index, expectedOutput)
}

// loadExpectedOutputs reads every referenced expected_output_file once so we
// don't re-read it per submission.
func loadExpectedOutputs(loadedPolicy *policy.Policy) map[string]*string {
	out := make(map[string]*string)
	configDir := loadedPolicy.EffectiveConfigDir()
	for _, tc := range loadedPolicy.Run.TestCases {
		if tc.ExpectedOutputFile == "" {
			continue
		}
		if _, exists := out[tc.ExpectedOutputFile]; exists {
			continue
		}
		data, err := os.ReadFile(filepath.Join(configDir, "expected_outputs", tc.ExpectedOutputFile))
		if err != nil {
			out[tc.ExpectedOutputFile] = nil
			continue
		}
		s := string(data)
		out[tc.ExpectedOutputFile] = &s
	}
	return out
}

func readSourceFiles(sub domain.Submission) []sourceFile {
	files := make([]sourceFile, 0, len(sub.CFiles))
	names := append([]string(nil), sub.CFiles...)
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(sub.Path, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		files = append(files, sourceFile{Name: name, Content: string(data)})
	}
	return files
}
