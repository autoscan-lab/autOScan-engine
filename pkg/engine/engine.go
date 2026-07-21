package engine

import (
	internalengine "github.com/autoscan-lab/autoscan-engine/internal/engine"
	aipkg "github.com/autoscan-lab/autoscan-engine/pkg/ai"
	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

type CompileOption = internalengine.CompileOption

type Runner = internalengine.Runner

type RunnerCallbacks = internalengine.RunnerCallbacks

type Executor = internalengine.Executor

func WithOutputDir(dir string) CompileOption {
	return internalengine.WithOutputDir(dir)
}

func NewRunner(p *policy.Policy, opts ...CompileOption) (*Runner, error) {
	return internalengine.NewRunner(p, opts...)
}

func NewExecutor(p *policy.Policy, binaryDir string) *Executor {
	return internalengine.NewExecutor(p, binaryDir)
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
