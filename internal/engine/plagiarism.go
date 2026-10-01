package engine

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
)

func CompareFiles(fileA, fileB string, fpA, fpB domain.FileFingerprint, cfg domain.CompareConfig) domain.PlagiarismResult {
	tiles := greedyStringTiling(fpA.Tokens, fpB.Tokens, cfg.MinMatchTokens)
	matched := tiledTokens(tiles)
	score := 0.0
	if total := fpA.TokenCount + fpB.TokenCount; total > 0 {
		score = 2 * float64(matched) / float64(total)
	}

	return domain.PlagiarismResult{
		FileA:             fileA,
		FileB:             fileB,
		SimilarityPercent: score * 100,
		Flagged:           score >= cfg.ScoreThreshold,
		Matches:           tileMatches(fpA, fpB, tiles),
	}
}

func tileMatches(fpA, fpB domain.FileFingerprint, tiles []tile) []domain.TileMatch {
	if len(tiles) == 0 {
		return nil
	}

	result := make([]domain.TileMatch, 0, len(tiles))
	for _, t := range tiles {
		result = append(result, domain.TileMatch{
			Hash:   fmt.Sprintf("t%d-%d", t.a, t.b),
			SpansA: convertSpans(fpA, []domain.Span{tileSpan(fpA, t.a, t.length)}),
			SpansB: convertSpans(fpB, []domain.Span{tileSpan(fpB, t.b, t.length)}),
		})
	}
	return result
}

type SubmissionFingerprint struct {
	FP  domain.FileFingerprint
	Err error
}

// The returned slice is index-aligned with submissions.
func FingerprintSubmissions(submissions []domain.Submission, srcFile string, cfg domain.CompareConfig) []SubmissionFingerprint {
	prints := make([]SubmissionFingerprint, len(submissions))
	parallelForEach(len(submissions), func(i int) {
		path := filepath.Join(submissions[i].Path, srcFile)
		fp, err := FingerprintFile(path, cfg)
		prints[i] = SubmissionFingerprint{FP: fp, Err: err}
	})
	return prints
}

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
