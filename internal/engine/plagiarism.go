package engine

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
)

func CompareFiles(fileA, fileB string, fpA, fpB domain.FileFingerprint, cfg domain.CompareConfig) domain.PlagiarismResult {
	exactMatches := countIntersection(fpA.FunctionHashes, fpB.FunctionHashes)
	windowMatches := countIntersection(fpA.WindowHashes, fpB.WindowHashes)
	windowUnion := unionCount(fpA.WindowHashes, fpB.WindowHashes)
	perFuncScore := avgBestFunctionSimilarity(fpA.FunctionWindows, fpB.FunctionWindows)

	score := jaccard(windowMatches, windowUnion)
	matches := extractWindowMatches(fpA, fpB)

	return domain.PlagiarismResult{
		FileA:             fileA,
		FileB:             fileB,
		FunctionCountA:    fpA.FunctionCount,
		FunctionCountB:    fpB.FunctionCount,
		ExactMatches:      exactMatches,
		WindowMatches:     windowMatches,
		WindowUnion:       windowUnion,
		WindowJaccard:     score,
		PerFuncSimilarity: perFuncScore,
		Flagged:           score >= cfg.ScoreThreshold,
		Matches:           matches,
	}
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

func ComputeSimilarityForProcess(submissions []domain.Submission, srcFile string, cfg domain.CompareConfig) ([]domain.SimilarityPairResult, error) {
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
			return nil, fmt.Errorf("no fingerprints created (%d failures). check source file selection and parseability", failures)
		}
		return []domain.SimilarityPairResult{}, nil
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
				AIndex: a.idx,
				BIndex: b.idx,
				Result: res,
			})
		}
	}

	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].Result.WindowJaccard > pairs[j].Result.WindowJaccard
	})

	return pairs, nil
}
