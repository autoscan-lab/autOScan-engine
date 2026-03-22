package engine

import (
	"context"
	"time"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

// Runner orchestrates the full grading pipeline.
type Runner struct {
	policy    *policy.Policy
	discovery *DiscoveryEngine
	compiler  *CompileEngine
	scanner   *ScanEngine
}

// RunnerCallbacks allows the TUI to receive progress updates.
type RunnerCallbacks struct {
	OnDiscoveryComplete func(submissions []domain.Submission)
	OnCompileComplete   func(sub domain.Submission, result domain.CompileResult)
	OnScanComplete      func(sub domain.Submission, result domain.ScanResult)
	OnAllComplete       func(report domain.RunReport)
}

// NewRunner creates a new runner with all engines.
func NewRunner(p *policy.Policy, opts ...CompileOption) (*Runner, error) {
	compiler, err := NewCompileEngine(p, opts...)
	if err != nil {
		return nil, err
	}

	return &Runner{
		policy:    p,
		discovery: NewDiscoveryEngine(p),
		compiler:  compiler,
		scanner:   NewScanEngine(p),
	}, nil
}

// Cleanup cleans up resources (temp directories).
func (r *Runner) Cleanup() error {
	return r.compiler.Cleanup()
}

// Run executes the full grading pipeline.
func (r *Runner) Run(ctx context.Context, root string, callbacks RunnerCallbacks) (*domain.RunReport, error) {
	startedAt := time.Now()

	// Discovery phase
	submissions, err := r.discovery.Discover(root)
	if err != nil {
		return nil, err
	}

	if callbacks.OnDiscoveryComplete != nil {
		callbacks.OnDiscoveryComplete(submissions)
	}

	// Compile phase
	compileResults := r.compiler.CompileAll(ctx, submissions, func(sub domain.Submission, result domain.CompileResult) {
		if callbacks.OnCompileComplete != nil {
			callbacks.OnCompileComplete(sub, result)
		}
	})

	// Scan phase
	scanResults := r.scanner.ScanAll(submissions, func(sub domain.Submission, result domain.ScanResult) {
		if callbacks.OnScanComplete != nil {
			callbacks.OnScanComplete(sub, result)
		}
	})

	// Build results
	results := make([]domain.SubmissionResult, len(submissions))
	for i := range submissions {
		results[i] = domain.NewSubmissionResult(
			submissions[i],
			compileResults[i],
			scanResults[i],
		)
	}

	finishedAt := time.Now()

	// Create report
	report := domain.NewRunReport(
		r.policy.Name,
		root,
		startedAt,
		finishedAt,
		results,
	)

	if callbacks.OnAllComplete != nil {
		callbacks.OnAllComplete(report)
	}

	return &report, nil
}
