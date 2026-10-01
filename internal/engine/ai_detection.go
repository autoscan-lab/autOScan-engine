package engine

import (
	"fmt"
	"sort"

	aipkg "github.com/autoscan-lab/autoscan-engine/pkg/ai"
	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
)

type dictionaryFingerprint struct {
	entry aipkg.Entry
	fp    domain.FileFingerprint
}

func ComputeAIDetectionFromFingerprints(submissions []domain.Submission, prints []SubmissionFingerprint, srcFile string, dict *aipkg.Dictionary, cfg domain.CompareConfig) (domain.AIDetectionReport, error) {
	report := domain.AIDetectionReport{
		SourceFile: srcFile,
	}

	if dict == nil {
		return report, fmt.Errorf("ai dictionary is nil")
	}

	report.DictionaryEntryCount = len(dict.Entries)
	dictFPs, dictErrors := fingerprintDictionary(dict, cfg)
	report.DictionaryUsable = len(dictFPs)
	report.DictionaryErrors = dictErrors

	if len(dictFPs) == 0 {
		return report, fmt.Errorf("ai dictionary has no usable entries")
	}

	results := make([]domain.AISubmissionResult, len(submissions))
	parallelForEach(len(submissions), func(i int) {
		sub := submissions[i]
		if prints[i].Err != nil {
			results[i] = domain.AISubmissionResult{
				SubmissionID: sub.ID,
				SourceFile:   srcFile,
				ParseError:   prints[i].Err.Error(),
			}
			return
		}
		fp := prints[i].FP

		matches := compareSubmissionToDictionary(fp, dictFPs, cfg)
		bestScore := 0.0
		flagged := false
		for _, m := range matches {
			if m.Score > bestScore {
				bestScore = m.Score
			}
			if m.Flagged {
				flagged = true
			}
		}

		results[i] = domain.AISubmissionResult{
			SubmissionID:  sub.ID,
			SourceFile:    srcFile,
			FunctionCount: fp.FunctionCount,
			MatchCount:    len(matches),
			BestScore:     bestScore,
			Flagged:       flagged,
			Matches:       matches,
		}
	})

	sort.Slice(results, func(i, j int) bool {
		if results[i].Flagged != results[j].Flagged {
			return results[i].Flagged
		}
		if results[i].BestScore != results[j].BestScore {
			return results[i].BestScore > results[j].BestScore
		}
		return results[i].SubmissionID < results[j].SubmissionID
	})

	report.Submissions = results
	return report, nil
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

func compareSubmissionToDictionary(subFP domain.FileFingerprint, dictFPs []dictionaryFingerprint, cfg domain.CompareConfig) []domain.AIDictionaryMatch {
	matches := make([]domain.AIDictionaryMatch, 0, len(dictFPs))

	for _, d := range dictFPs {
		tiles := greedyStringTiling(subFP.Tokens, d.fp.Tokens, cfg.MinMatchTokens)
		if len(tiles) == 0 {
			continue
		}
		// Containment: how much of the entry appears in the submission.
		score := float64(tiledTokens(tiles)) / float64(d.fp.TokenCount)

		spans := make([]domain.Span, 0, len(tiles))
		for _, t := range tiles {
			spans = append(spans, tileSpan(subFP, t.a, t.length))
		}
		matches = append(matches, domain.AIDictionaryMatch{
			EntryID: d.entry.ID,
			Title:   d.entry.Title,
			Score:   score,
			Flagged: score >= cfg.ScoreThreshold,
			Spans:   convertSpans(subFP, spans),
		})
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Flagged != matches[j].Flagged {
			return matches[i].Flagged
		}
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].EntryID < matches[j].EntryID
	})

	return matches
}
