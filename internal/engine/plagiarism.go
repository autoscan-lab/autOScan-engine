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

func ComputeSimilarityForProcess(submissions []domain.Submission, srcFile string, cfg domain.CompareConfig) (domain.SimilarityReport, error) {
	report := domain.SimilarityReport{SourceFile: srcFile}

	type fingerprintItem struct {
		idx int
		fp  domain.FileFingerprint
	}

	var fps []fingerprintItem
	var failures int

	for i, sub := range submissions {
		path := filepath.Join(sub.Path, srcFile)
		fp, err := FingerprintFile(path, cfg)
		if err != nil {
			failures++
			continue
		}
		fps = append(fps, fingerprintItem{idx: i, fp: fp})
	}

	if len(fps) < 2 {
		if failures > 0 && len(fps) == 0 {
			return report, fmt.Errorf("no fingerprints created (%d failures). check source file selection and parseability", failures)
		}
		report.Pairs = []domain.SimilarityPairResult{}
		return report, nil
	}

	var pairs []domain.SimilarityPairResult
	for i := 0; i < len(fps); i++ {
		for j := i + 1; j < len(fps); j++ {
			a := fps[i]
			b := fps[j]
			res := CompareFiles(
				filepath.Base(submissions[a.idx].ID),
				filepath.Base(submissions[b.idx].ID),
				a.fp,
				b.fp,
				cfg,
			)
			pairs = append(pairs, domain.SimilarityPairResult{
				A:                submissions[a.idx].ID,
				B:                submissions[b.idx].ID,
				PlagiarismResult: res,
			})
		}
	}

	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].SimilarityPercent > pairs[j].SimilarityPercent
	})

	report.Pairs = pairs
	return report, nil
}
