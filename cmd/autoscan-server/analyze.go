package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	aipkg "github.com/autoscan-lab/autoscan-engine/pkg/ai"
	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/engine"
)

// Defaults match conventional code-similarity tuning. Could be made
// configurable via form fields later if needed.
var defaultCompareConfig = domain.CompareConfig{
	WindowSize:     5,
	MinFuncTokens:  20,
	ScoreThreshold: 0.7,
}

type analysisOptions struct {
	IncludeSimilarity       bool
	IncludeAIDetection      bool
	SimilarityIncludeSpans  bool
	AIDetectionIncludeSpans bool
	SimilarityMinScore      float64
	AIDetectionMinScore     float64
}

// runAnalysis computes similarity and/or AI detection from a run's submissions.
func runAnalysis(cfg config, sourceFile string, submissions []domain.Submission, opts analysisOptions) (*domain.SimilarityReport, *domain.AIDetectionReport, error) {
	if !opts.IncludeSimilarity && !opts.IncludeAIDetection {
		return nil, nil, nil
	}

	if sourceFile == "" {
		return nil, nil, &httpError{status: 400, msg: "policy has no compile.source_file; cannot run similarity/ai detection"}
	}

	var sim *domain.SimilarityReport
	var ai *domain.AIDetectionReport

	if opts.IncludeSimilarity {
		result, err := engine.ComputeSimilarityForProcess(submissions, sourceFile, defaultCompareConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("computing similarity: %w", err)
		}
		trimSimilarityReport(&result, opts.SimilarityIncludeSpans, opts.SimilarityMinScore)
		sim = &result
	}

	if opts.IncludeAIDetection {
		dict, err := loadAIDictionary(cfg)
		if err != nil {
			return nil, nil, err
		}
		result, err := engine.ComputeAIDetectionForProcess(submissions, sourceFile, dict, defaultCompareConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("computing ai detection: %w", err)
		}
		trimAIDetectionReport(&result, opts.AIDetectionIncludeSpans, opts.AIDetectionMinScore)
		ai = &result
	}

	return sim, ai, nil
}

func trimSimilarityReport(report *domain.SimilarityReport, includeSpans bool, minScore float64) {
	if report == nil {
		return
	}
	if minScore > 0 {
		filtered := report.Pairs[:0]
		for _, pair := range report.Pairs {
			if pair.SimilarityPercent >= minScore {
				filtered = append(filtered, pair)
			}
		}
		report.Pairs = filtered
	}
	if includeSpans {
		return
	}
	for index := range report.Pairs {
		report.Pairs[index].Matches = nil
	}
}

func trimAIDetectionReport(report *domain.AIDetectionReport, includeSpans bool, minScore float64) {
	if report == nil {
		return
	}
	if minScore > 0 {
		filtered := report.Submissions[:0]
		for _, submission := range report.Submissions {
			if submission.BestScore*100 >= minScore {
				filtered = append(filtered, submission)
			}
		}
		report.Submissions = filtered
	}
	if includeSpans {
		return
	}
	for submissionIndex := range report.Submissions {
		for matchIndex := range report.Submissions[submissionIndex].Matches {
			report.Submissions[submissionIndex].Matches[matchIndex].Spans = nil
		}
	}
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
