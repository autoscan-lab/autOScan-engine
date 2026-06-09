package engine

import (
	"context"

	internalengine "github.com/autoscan-lab/autoscan-engine/internal/engine"
	aipkg "github.com/autoscan-lab/autoscan-engine/pkg/ai"
	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

const (
	DefaultTimeout = internalengine.DefaultTimeout
	DefaultWorkers = internalengine.DefaultWorkers
)

type CompileOption = internalengine.CompileOption

type Runner = internalengine.Runner

type RunnerCallbacks = internalengine.RunnerCallbacks

type Executor = internalengine.Executor

func WithWorkers(n int) CompileOption {
	return internalengine.WithWorkers(n)
}

func WithOutputDir(dir string) CompileOption {
	return internalengine.WithOutputDir(dir)
}

func WithShortNames(enabled bool) CompileOption {
	return internalengine.WithShortNames(enabled)
}

func NewRunner(p *policy.Policy, opts ...CompileOption) (*Runner, error) {
	return internalengine.NewRunner(p, opts...)
}

func NewExecutorWithOptions(p *policy.Policy, binaryDir string, shortNames bool) *Executor {
	return internalengine.NewExecutorWithOptions(p, binaryDir, shortNames)
}

func ComputeSimilarityForProcess(submissions []domain.Submission, srcFile string, cfg domain.CompareConfig) (domain.SimilarityReport, error) {
	return internalengine.ComputeSimilarityForProcess(submissions, srcFile, cfg)
}

func ComputeAIDetectionForProcess(submissions []domain.Submission, srcFile string, dict *aipkg.Dictionary, cfg domain.CompareConfig) (domain.AIDetectionReport, error) {
	return internalengine.ComputeAIDetectionForProcess(submissions, srcFile, dict, cfg)
}

// SubmissionFingerprint lets callers fingerprint submissions once and feed the
// result to both similarity and AI detection instead of fingerprinting twice.
type SubmissionFingerprint = internalengine.SubmissionFingerprint

func FingerprintSubmissions(submissions []domain.Submission, srcFile string, cfg domain.CompareConfig) []SubmissionFingerprint {
	return internalengine.FingerprintSubmissions(submissions, srcFile, cfg)
}

func ComputeSimilarityFromFingerprints(submissions []domain.Submission, prints []SubmissionFingerprint, srcFile string, cfg domain.CompareConfig) (domain.SimilarityReport, error) {
	return internalengine.ComputeSimilarityFromFingerprints(submissions, prints, srcFile, cfg)
}

func ComputeAIDetectionFromFingerprints(submissions []domain.Submission, prints []SubmissionFingerprint, srcFile string, dict *aipkg.Dictionary, cfg domain.CompareConfig) (domain.AIDetectionReport, error) {
	return internalengine.ComputeAIDetectionFromFingerprints(submissions, prints, srcFile, dict, cfg)
}

func Run(r *Runner, ctx context.Context, root string, callbacks RunnerCallbacks) (*domain.RunReport, error) {
	return r.Run(ctx, root, callbacks)
}
