package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	aipkg "github.com/autoscan-lab/autoscan-engine/pkg/ai"
	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/engine"
	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

// Defaults match conventional code-similarity tuning. Could be made
// configurable via form fields later if needed.
var defaultCompareConfig = domain.CompareConfig{
	WindowSize:     5,
	MinFuncTokens:  20,
	ScoreThreshold: 0.7,
}

// runAnalysis adds similarity and/or AI detection to an existing grading
// response, reusing the same submissions discovered during the run.
func runAnalysis(_ context.Context, cfg config, loadedPolicy *policy.Policy, report *domain.RunReport, includeSimilarity, includeAIDetection bool) (*domain.SimilarityReport, *domain.AIDetectionReport, error) {
	if !includeSimilarity && !includeAIDetection {
		return nil, nil, nil
	}

	srcFile := loadedPolicy.Compile.SourceFile
	if srcFile == "" {
		return nil, nil, &httpError{status: 400, msg: "policy has no compile.source_file; cannot run similarity/ai detection"}
	}

	subs := make([]domain.Submission, len(report.Results))
	for i, r := range report.Results {
		subs[i] = r.Submission
	}

	var sim *domain.SimilarityReport
	var ai *domain.AIDetectionReport

	if includeSimilarity {
		result, err := engine.ComputeSimilarityForProcess(subs, srcFile, defaultCompareConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("computing similarity: %w", err)
		}
		sim = &result
	}

	if includeAIDetection {
		dict, err := loadAIDictionary(cfg)
		if err != nil {
			return nil, nil, err
		}
		result, err := engine.ComputeAIDetectionForProcess(subs, srcFile, dict, defaultCompareConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("computing ai detection: %w", err)
		}
		ai = &result
	}

	return sim, ai, nil
}

func loadAIDictionary(cfg config) (*aipkg.Dictionary, error) {
	path := filepath.Join(cfg.currentDir, "ai_dictionary.yaml")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &httpError{status: 503, msg: "no ai_dictionary.yaml in active config; upload one to R2 and re-run /setup"}
		}
		return nil, err
	}
	dict, err := aipkg.LoadDictionary(path)
	if err != nil {
		return nil, fmt.Errorf("loading ai dictionary: %w", err)
	}
	return dict, nil
}
