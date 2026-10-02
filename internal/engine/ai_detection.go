package engine

import (
	"fmt"
	"sort"

	aipkg "github.com/autoscan-lab/autoscan-engine/pkg/ai"
	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
)

const (
	// A pattern counts once at least half of it appears; a two-line idiom alone is not the pattern.
	aiPatternMinShare = 0.5
	// Flag a submission when this share of its code is dictionary patterns.
	aiFlagShare = 0.25
)

type dictionaryFingerprint struct {
	entry aipkg.Entry
	fp    domain.FileFingerprint
}

// A missing or unusable dictionary only disables pattern matching; style and tells still run.
func ComputeAIDetectionFromFingerprints(submissions []domain.Submission, prints []SubmissionFingerprint, srcFile string, dict *aipkg.Dictionary, cfg domain.CompareConfig) (domain.AIDetectionReport, error) {
	report := domain.AIDetectionReport{
		Method:     domain.AIDetectionMethod,
		SourceFile: srcFile,
	}
	if len(prints) != len(submissions) {
		return report, fmt.Errorf("fingerprint count must match submission count")
	}

	var dictFPs []dictionaryFingerprint
	if dict != nil {
		report.DictionaryEntryCount = len(dict.Entries)
		var dictErrors []domain.AIDictionaryEntryError
		dictFPs, dictErrors = fingerprintDictionary(dict, cfg)
		report.DictionaryUsable = len(dictFPs)
		report.DictionaryErrors = dictErrors
	}

	results := make([]domain.AISubmissionResult, len(submissions))
	tokens := make([][][]metricToken, len(submissions))
	parallelForEach(len(submissions), func(i int) {
		sub := submissions[i]
		style, tells, sourceTokens := analyzeSubmissionStyle(sub)
		tokens[i] = sourceTokens
		result := domain.AISubmissionResult{
			SubmissionID: sub.ID,
			SourceFile:   srcFile,
			Style:        style,
			Tells:        tells,
		}
		if prints[i].Err != nil {
			result.ParseError = prints[i].Err.Error()
		} else {
			fp := prints[i].FP
			matches, covered := compareSubmissionToDictionary(fp, dictFPs, cfg)
			result.FunctionCount = fp.FunctionCount
			result.MatchCount = len(matches)
			result.Matches = matches
			if fp.TokenCount > 0 {
				result.Score = float64(covered) / float64(fp.TokenCount)
			}
		}
		result.Flagged = result.Score >= aiFlagShare || (style != nil && style.Flagged)
		for _, tell := range tells {
			result.Flagged = result.Flagged || tell.Flagged
		}
		result.AIScore = overallAIScore(result, len(dictFPs) > 0)
		results[i] = result
	})
	setTokenMetrics(results, tokens)
	setStylePercentiles(results)

	sortAIResults(results)
	report.Submissions = results
	return report, nil
}

func sortAIResults(results []domain.AISubmissionResult) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Flagged != results[j].Flagged {
			return results[i].Flagged
		}
		if (results[i].AIScore != nil) != (results[j].AIScore != nil) {
			return results[i].AIScore != nil
		}
		if results[i].AIScore != nil && results[i].AIScore.Score != results[j].AIScore.Score {
			return results[i].AIScore.Score > results[j].AIScore.Score
		}
		si, sj := 0.0, 0.0
		if results[i].Style != nil {
			si = results[i].Style.Score
		}
		if results[j].Style != nil {
			sj = results[j].Style.Score
		}
		if si != sj {
			return si > sj
		}
		return results[i].SubmissionID < results[j].SubmissionID
	})
}

// Similarity links provide context without propagating AI flags.
func LinkSimilarToFlagged(report *domain.AIDetectionReport, similarity *domain.SimilarityReport) {
	if report == nil || similarity == nil {
		return
	}
	index := make(map[string]int, len(report.Submissions))
	ownFlag := make(map[string]bool, len(report.Submissions))
	for i, sub := range report.Submissions {
		index[sub.SubmissionID] = i
		ownFlag[sub.SubmissionID] = sub.Score >= aiFlagShare || (sub.Style != nil && sub.Style.Flagged)
		for _, tell := range sub.Tells {
			ownFlag[sub.SubmissionID] = ownFlag[sub.SubmissionID] || tell.Flagged
		}
		report.Submissions[i].SimilarToFlagged = nil
	}
	link := func(from, to string, percent float64) {
		i, ok := index[from]
		if !ok || !ownFlag[to] {
			return
		}
		sub := &report.Submissions[i]
		sub.SimilarToFlagged = append(sub.SimilarToFlagged, domain.AISimilarLink{SubmissionID: to, SimilarityPercent: percent})
	}
	for _, pair := range similarity.Pairs {
		if !pair.Flagged {
			continue
		}
		link(pair.A, pair.B, pair.SimilarityPercent)
		link(pair.B, pair.A, pair.SimilarityPercent)
	}
	for i := range report.Submissions {
		links := report.Submissions[i].SimilarToFlagged
		sort.Slice(links, func(a, b int) bool { return links[a].SimilarityPercent > links[b].SimilarityPercent })
	}
	sortAIResults(report.Submissions)
}

func setStylePercentiles(results []domain.AISubmissionResult) {
	if len(results) < 2 {
		return
	}
	for i := range results {
		if !hasReviewFeatures(results[i].Style) {
			continue
		}
		below := 0
		peers := 0
		for j := range results {
			if j == i || !hasReviewFeatures(results[j].Style) {
				continue
			}
			peers++
			if results[j].Style.Score < results[i].Style.Score {
				below++
			}
		}
		if peers > 0 {
			results[i].Style.CohortSize = peers
			results[i].Style.Percentile = round3(float64(below) / float64(peers))
		}
	}
}

func hasReviewFeatures(style *domain.AIStyleReport) bool {
	if style == nil {
		return false
	}
	for _, feature := range style.Features {
		if feature.Evidence == evidenceReview {
			return true
		}
	}
	return false
}

func fingerprintDictionary(dict *aipkg.Dictionary, cfg domain.CompareConfig) ([]dictionaryFingerprint, []domain.AIDictionaryEntryError) {
	type entrySlot struct {
		item *dictionaryFingerprint
		err  *domain.AIDictionaryEntryError
	}
	slots := make([]entrySlot, len(dict.Entries))
	parallelForEach(len(dict.Entries), func(i int) {
		e := dict.Entries[i]
		// Entries are curated snippets, so short helper functions still count.
		fp, err := fingerprintContent([]byte(e.Code), 0)
		if err != nil {
			slots[i] = entrySlot{err: &domain.AIDictionaryEntryError{EntryID: e.ID, Err: err.Error()}}
			return
		}
		if fp.TokenCount < cfg.MinMatchTokens {
			slots[i] = entrySlot{err: &domain.AIDictionaryEntryError{EntryID: e.ID, Err: "entry has no function long enough to match"}}
			return
		}
		slots[i] = entrySlot{item: &dictionaryFingerprint{entry: e, fp: fp}}
	})

	items := make([]dictionaryFingerprint, 0, len(dict.Entries))
	errs := make([]domain.AIDictionaryEntryError, 0)
	for i := range slots {
		if slots[i].item != nil {
			items = append(items, *slots[i].item)
		}
		if slots[i].err != nil {
			errs = append(errs, *slots[i].err)
		}
	}

	return items, errs
}

// Returns the patterns substantially present and how many of the submission's tokens they cover.
func compareSubmissionToDictionary(subFP domain.FileFingerprint, dictFPs []dictionaryFingerprint, cfg domain.CompareConfig) ([]domain.AIDictionaryMatch, int) {
	matches := make([]domain.AIDictionaryMatch, 0, len(dictFPs))
	covered := make([]bool, len(subFP.Tokens))

	for _, d := range dictFPs {
		tiles := greedyStringTiling(subFP.Tokens, d.fp.Tokens, cfg.MinMatchTokens)
		share := float64(tiledTokens(tiles)) / float64(d.fp.TokenCount)
		if share < aiPatternMinShare {
			continue
		}

		spans := make([]domain.Span, 0, len(tiles))
		for _, t := range tiles {
			spans = append(spans, tileSpan(subFP, t.a, t.length))
			for k := t.a; k < t.a+t.length; k++ {
				covered[k] = true
			}
		}
		matches = append(matches, domain.AIDictionaryMatch{
			EntryID: d.entry.ID,
			Title:   d.entry.Title,
			Score:   share,
			Spans:   convertSpans(subFP, spans),
		})
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].EntryID < matches[j].EntryID
	})

	count := 0
	for _, c := range covered {
		if c {
			count++
		}
	}
	return matches, count
}
