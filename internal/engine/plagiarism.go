package engine

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
)

func CompareFiles(fileA, fileB string, fpA, fpB domain.FileFingerprint, cfg domain.CompareConfig) domain.PlagiarismResult {
	exactMatches := countIntersection(fpA.FunctionHashes, fpB.FunctionHashes)
	windowMatches := countIntersection(fpA.WindowHashes, fpB.WindowHashes)
	windowUnion := unionCount(fpA.WindowHashes, fpB.WindowHashes)
	windowScore := jaccard(windowMatches, windowUnion)
	perFuncScore := avgBestFunctionSimilarity(fpA.FunctionWindows, fpB.FunctionWindows)
	combinedScore := combinedSimilarityScore(windowScore, perFuncScore, fpA.FunctionCount, fpB.FunctionCount)
	similarityPercent := combinedScore * 100

	matches := extractWindowMatches(fpA, fpB)

	return domain.PlagiarismResult{
		FileA:             fileA,
		FileB:             fileB,
		FunctionCountA:    fpA.FunctionCount,
		FunctionCountB:    fpB.FunctionCount,
		ExactMatches:      exactMatches,
		WindowMatches:     windowMatches,
		WindowUnion:       windowUnion,
		SimilarityPercent: similarityPercent,
		Flagged:           combinedScore >= cfg.ScoreThreshold,
		Matches:           matches,
	}
}

func combinedSimilarityScore(windowScore, perFuncScore float64, functionCountA, functionCountB int) float64 {
	if functionCountA == 0 || functionCountB == 0 {
		return math.Max(0, math.Min(1, windowScore))
	}

	// Whole-file overlap is the stronger base signal; per-function overlap
	// adds structural confidence without dominating the final score.
	score := (0.65 * windowScore) + (0.35 * perFuncScore)
	return math.Max(0, math.Min(1, score))
}

func extractWindowMatches(fpA, fpB domain.FileFingerprint) []domain.WindowMatch {
	matches := make([]string, 0, len(fpA.WindowHashes))
	for k := range fpA.WindowHashes {
		if _, ok := fpB.WindowHashes[k]; ok {
			matches = append(matches, k)
		}
	}
	if len(matches) == 0 {
		return nil
	}
	sort.Strings(matches)

	result := make([]domain.WindowMatch, 0, len(matches))
	for _, hash := range matches {
		mergedA := mergeSpans(fpA.WindowSpans[hash])
		mergedB := mergeSpans(fpB.WindowSpans[hash])
		spansA := convertSpans(fpA, mergedA)
		spansB := convertSpans(fpB, mergedB)
		result = append(result, domain.WindowMatch{
			Hash:   hash[:8], // First 8 chars for display
			SpansA: spansA,
			SpansB: spansB,
		})
	}
	return result
}

// SubmissionFingerprint is one submission's fingerprint, or the error that
// prevented it. Computing these once lets similarity and AI detection share a
// single fingerprint pass instead of each fingerprinting every submission.
type SubmissionFingerprint struct {
	FP  domain.FileFingerprint
	Err error
}

// FingerprintSubmissions fingerprints each submission's srcFile once, in
// parallel. The returned slice is index-aligned with submissions.
func FingerprintSubmissions(submissions []domain.Submission, srcFile string, cfg domain.CompareConfig) []SubmissionFingerprint {
	prints := make([]SubmissionFingerprint, len(submissions))
	parallelForEach(len(submissions), func(i int) {
		path := filepath.Join(submissions[i].Path, srcFile)
		fp, err := FingerprintFile(path, cfg)
		prints[i] = SubmissionFingerprint{FP: fp, Err: err}
	})
	return prints
}

// ComputeSimilarityFromFingerprints runs the pairwise comparison over a
// precomputed set of fingerprints (index-aligned with submissions).
func ComputeSimilarityFromFingerprints(submissions []domain.Submission, prints []SubmissionFingerprint, srcFile string, cfg domain.CompareConfig) (domain.SimilarityReport, error) {
	report := domain.SimilarityReport{SourceFile: srcFile}

	type fingerprintItem struct {
		idx int
		fp  domain.FileFingerprint
	}

	var fps []fingerprintItem
	var failures int
	for i := range prints {
		if prints[i].Err == nil {
			fps = append(fps, fingerprintItem{idx: i, fp: prints[i].FP})
		} else {
			failures++
		}
	}

	if len(fps) < 2 {
		if failures > 0 && len(fps) == 0 {
			return report, fmt.Errorf("no fingerprints created (%d failures). check source file selection and parseability", failures)
		}
		report.Pairs = []domain.SimilarityPairResult{}
		return report, nil
	}

	type pairIndex struct{ i, j int }
	jobs := make([]pairIndex, 0, len(fps)*(len(fps)-1)/2)
	for i := 0; i < len(fps); i++ {
		for j := i + 1; j < len(fps); j++ {
			jobs = append(jobs, pairIndex{i: i, j: j})
		}
	}

	pairs := make([]domain.SimilarityPairResult, len(jobs))
	parallelForEach(len(jobs), func(k int) {
		a := fps[jobs[k].i]
		b := fps[jobs[k].j]
		res := CompareFiles(
			filepath.Base(submissions[a.idx].ID),
			filepath.Base(submissions[b.idx].ID),
			a.fp,
			b.fp,
			cfg,
		)
		pairs[k] = domain.SimilarityPairResult{
			A:                submissions[a.idx].ID,
			B:                submissions[b.idx].ID,
			PlagiarismResult: res,
		}
	})

	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].SimilarityPercent > pairs[j].SimilarityPercent
	})

	report.Pairs = pairs
	return report, nil
}
