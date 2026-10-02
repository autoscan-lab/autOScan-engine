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

var defaultCompareConfig = domain.CompareConfig{
	MinMatchTokens: 12,
	MinFuncTokens:  20,
	ScoreThreshold: 0.6,
}

type analysisOptions struct {
	IncludeSimilarity       bool
	IncludeAIDetection      bool
	SimilarityIncludeSpans  bool
	AIDetectionIncludeSpans bool
	SimilarityMinScore      float64
	AIDetectionMinScore     float64
}

func runAnalysis(configDir, sourceFile string, submissions []domain.Submission, opts analysisOptions) (*domain.SimilarityReport, *domain.AIDetectionReport, error) {
	if !opts.IncludeSimilarity && !opts.IncludeAIDetection {
		return nil, nil, nil
	}

	if sourceFile == "" {
		return nil, nil, &httpError{status: 400, msg: "policy has no compile.source_file; cannot run similarity/ai detection"}
	}

	var sim *domain.SimilarityReport
	var ai *domain.AIDetectionReport

	prints := engine.FingerprintSubmissions(submissions, sourceFile, defaultCompareConfig)

	if opts.IncludeSimilarity {
		result, err := engine.ComputeSimilarityFromFingerprints(submissions, prints, sourceFile, defaultCompareConfig, solutionFingerprint(configDir, sourceFile))
		if err != nil {
			return nil, nil, fmt.Errorf("computing similarity: %w", err)
		}
		sim = &result
	}

	if opts.IncludeAIDetection {
		dict, err := loadAIDictionary(configDir)
		if err != nil {
			return nil, nil, err
		}
		result, err := engine.ComputeAIDetectionFromFingerprints(submissions, prints, sourceFile, dict, defaultCompareConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("computing ai detection: %w", err)
		}
		ai = &result
	}

	engine.LinkSimilarToFlagged(ai, sim)
	trimSimilarityReport(sim, opts.SimilarityIncludeSpans, opts.SimilarityMinScore)
	trimAIDetectionReport(ai, opts.AIDetectionIncludeSpans, opts.AIDetectionMinScore)
	return sim, ai, nil
}

// The assignment's reference solution (solution/<source_file>), or nil when it has none.
func solutionFingerprint(configDir, sourceFile string) *domain.FileFingerprint {
	fp, err := engine.FingerprintFile(filepath.Join(configDir, "solution", sourceFile), defaultCompareConfig)
	if err != nil || fp.TokenCount == 0 {
		return nil
	}
	return &fp
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
		if report.Solution != nil {
			solutionPairs := report.Solution.Pairs[:0]
			for _, pair := range report.Solution.Pairs {
				if pair.SimilarityPercent >= minScore {
					solutionPairs = append(solutionPairs, pair)
				}
			}
			report.Solution.Pairs = solutionPairs
		}
	}
	if includeSpans {
		return
	}
	for index := range report.Pairs {
		report.Pairs[index].Matches = nil
	}
	if report.Solution != nil {
		for index := range report.Solution.Pairs {
			report.Solution.Pairs[index].Matches = nil
		}
	}
}

func trimAIDetectionReport(report *domain.AIDetectionReport, includeSpans bool, minScore float64) {
	if report == nil {
		return
	}
	if minScore > 0 {
		filtered := report.Submissions[:0]
		for _, submission := range report.Submissions {
			if submission.Score*100 >= minScore || submission.Flagged {
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

// A missing dictionary disables pattern matching only; style signals and tells still run.
func loadAIDictionary(configDir string) (*aipkg.Dictionary, error) {
	path := filepath.Join(configDir, "ai_dictionary.yaml")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	dict, err := aipkg.LoadDictionary(path)
	if err != nil {
		return nil, fmt.Errorf("loading ai dictionary: %w", err)
	}
	return dict, nil
}
