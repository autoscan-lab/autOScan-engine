package engine

import (
	"fmt"
	"path/filepath"
	"sort"

	aipkg "github.com/autoscan-lab/autoscan-engine/pkg/ai"
	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
)

type dictionaryFingerprint struct {
	entry aipkg.Entry
	fp    domain.FileFingerprint
}

// ComputeAIDetectionForProcess compares each submission source file against dictionary fingerprints.
func ComputeAIDetectionForProcess(submissions []domain.Submission, srcFile string, dict *aipkg.Dictionary, cfg domain.CompareConfig) (domain.AIDetectionReport, error) {
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

	results := make([]domain.AISubmissionResult, 0, len(submissions))
	for _, sub := range submissions {
		filePath := filepath.Join(sub.Path, srcFile)
		fp, err := FingerprintFile(filePath, cfg)
		if err != nil {
			results = append(results, domain.AISubmissionResult{
				SubmissionID: sub.ID,
				SourceFile:   srcFile,
				ParseError:   err.Error(),
			})
			continue
		}

		matches := compareSubmissionToDictionary(fp, dictFPs, cfg)
		bestScore := 0.0
		flagged := false
		for _, m := range matches {
			if m.Jaccard > bestScore {
				bestScore = m.Jaccard
			}
			if m.Flagged {
				flagged = true
			}
		}

		results = append(results, domain.AISubmissionResult{
			SubmissionID:  sub.ID,
			SourceFile:    srcFile,
			FunctionCount: fp.FunctionCount,
			MatchCount:    len(matches),
			BestScore:     bestScore,
			Flagged:       flagged,
			Matches:       matches,
		})
	}

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
	items := make([]dictionaryFingerprint, 0, len(dict.Entries))
	errs := make([]domain.AIDictionaryEntryError, 0)

	for _, e := range dict.Entries {
		fp, err := fingerprintContent([]byte(e.Code), cfg)
		if err != nil {
			errs = append(errs, domain.AIDictionaryEntryError{
				EntryID: e.ID,
				Err:     err.Error(),
			})
			continue
		}
		if len(fp.WindowHashes) == 0 {
			errs = append(errs, domain.AIDictionaryEntryError{
				EntryID: e.ID,
				Err:     "entry produced no window fingerprints",
			})
			continue
		}
		items = append(items, dictionaryFingerprint{entry: e, fp: fp})
	}

	return items, errs
}

func compareSubmissionToDictionary(subFP domain.FileFingerprint, dictFPs []dictionaryFingerprint, cfg domain.CompareConfig) []domain.AIDictionaryMatch {
	matches := make([]domain.AIDictionaryMatch, 0, len(dictFPs))

	for _, d := range dictFPs {
		windowMatches := countIntersection(subFP.WindowHashes, d.fp.WindowHashes)
		windowUnion := unionCount(subFP.WindowHashes, d.fp.WindowHashes)
		score := jaccard(windowMatches, windowUnion)

		if windowMatches == 0 {
			continue
		}

		matches = append(matches, domain.AIDictionaryMatch{
			EntryID:  d.entry.ID,
			Category: d.entry.Category,
			Title:    d.entry.Title,
			Jaccard:  score,
			Flagged:  score >= cfg.ScoreThreshold,
			Spans:    extractSubmissionMatches(subFP, d.fp),
		})
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Flagged != matches[j].Flagged {
			return matches[i].Flagged
		}
		if matches[i].Jaccard != matches[j].Jaccard {
			return matches[i].Jaccard > matches[j].Jaccard
		}
		return matches[i].EntryID < matches[j].EntryID
	})

	return matches
}

func extractSubmissionMatches(subFP, dictFP domain.FileFingerprint) []domain.MatchSpan {
	if len(subFP.WindowHashes) == 0 || len(dictFP.WindowHashes) == 0 {
		return nil
	}

	hashes := make([]string, 0, len(subFP.WindowHashes))
	for h := range subFP.WindowHashes {
		if _, ok := dictFP.WindowHashes[h]; ok {
			hashes = append(hashes, h)
		}
	}
	if len(hashes) == 0 {
		return nil
	}

	sort.Strings(hashes)
	var spans []domain.Span
	for _, h := range hashes {
		spans = append(spans, subFP.WindowSpans[h]...)
	}
	return convertSpans(subFP, mergeSpans(spans))
}
