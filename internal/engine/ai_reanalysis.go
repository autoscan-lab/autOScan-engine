package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
)

// Saved dictionary matches and archive artifacts survive because their original inputs may be gone.
func ReanalyzeSavedAIDetection(ctx context.Context, payload []byte, cfg domain.CompareConfig) ([]byte, bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || fields == nil {
		return nil, false, fmt.Errorf("invalid saved result JSON")
	}
	var saved struct {
		SourceFile string `json:"source_file"`
		Results    []struct {
			Submission  domain.Submission `json:"submission"`
			SourceFiles []struct {
				Name    string `json:"name"`
				Content string `json:"content"`
			} `json:"source_files"`
		} `json:"results"`
		AI         *domain.AIDetectionReport `json:"ai_detection"`
		Similarity *domain.SimilarityReport  `json:"similarity"`
	}
	if err := json.Unmarshal(payload, &saved); err != nil {
		return nil, false, fmt.Errorf("invalid saved result: %w", err)
	}
	if saved.AI != nil && saved.AI.Method == domain.AIDetectionMethod {
		return payload, false, nil
	}
	if fields["results"] == nil || saved.SourceFile == "" || !safeSavedSourceName(saved.SourceFile) {
		return nil, false, fmt.Errorf("saved run has no usable results or source_file")
	}
	root, err := os.MkdirTemp("", "autoscan-ai-reanalysis-*")
	if err != nil {
		return nil, false, err
	}
	defer os.RemoveAll(root)
	submissions := make([]domain.Submission, len(saved.Results))
	seenIDs := map[string]bool{}
	for i, result := range saved.Results {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		id := result.Submission.ID
		if id == "" || seenIDs[id] {
			return nil, false, fmt.Errorf("missing or duplicate submission ID")
		}
		seenIDs[id] = true
		dir := filepath.Join(root, fmt.Sprint(i))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, false, err
		}
		files := make([]string, 0, len(result.SourceFiles))
		seenFiles := map[string]bool{}
		for _, file := range result.SourceFiles {
			if !safeSavedSourceName(file.Name) || seenFiles[file.Name] {
				return nil, false, fmt.Errorf("unsafe or duplicate saved source filename")
			}
			seenFiles[file.Name] = true
			full := filepath.Join(dir, filepath.FromSlash(file.Name))
			if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
				return nil, false, err
			}
			if err := os.WriteFile(full, []byte(file.Content), 0o600); err != nil {
				return nil, false, err
			}
			files = append(files, file.Name)
		}
		for _, expected := range result.Submission.CFiles {
			if !seenFiles[expected] {
				return nil, false, fmt.Errorf("saved source missing for submission %q", id)
			}
		}
		submissions[i] = domain.NewSubmission(id, dir, files)
	}
	prints := FingerprintSubmissions(submissions, saved.SourceFile, cfg)
	report, err := ComputeAIDetectionFromFingerprints(submissions, prints, saved.SourceFile, nil, cfg)
	if err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	oldRows := map[string]domain.AISubmissionResult{}
	if saved.AI != nil {
		report.DictionaryEntryCount = saved.AI.DictionaryEntryCount
		report.DictionaryUsable = saved.AI.DictionaryUsable
		report.DictionaryErrors = saved.AI.DictionaryErrors
		for _, row := range saved.AI.Submissions {
			oldRows[row.SubmissionID] = row
		}
	}
	for i := range report.Submissions {
		row := &report.Submissions[i]
		old, exists := oldRows[row.SubmissionID]
		if report.DictionaryUsable > 0 && !exists {
			return nil, false, fmt.Errorf("saved dictionary coverage missing for submission %q", row.SubmissionID)
		}
		row.Score, row.MatchCount, row.Matches = old.Score, old.MatchCount, old.Matches
		row.Flagged = row.Flagged || row.Score >= aiFlagShare
		for _, tell := range old.Tells {
			if tell.Kind == tellKindToolFile {
				row.Tells = append(row.Tells, tell)
				row.Flagged = row.Flagged || tell.Flagged
			}
		}
		row.AIScore = overallAIScore(*row, report.DictionaryUsable > 0)
	}
	LinkSimilarToFlagged(&report, saved.Similarity)
	sortAIResults(report.Submissions)
	fields["ai_detection"], err = json.Marshal(report)
	if err != nil {
		return nil, false, err
	}
	previous := ""
	if saved.AI != nil {
		previous = saved.AI.Method
	}
	fields["ai_reanalysis"], err = json.Marshal(struct {
		Method         string `json:"method"`
		PreviousMethod string `json:"previous_method,omitempty"`
		CompletedAt    string `json:"completed_at"`
	}{domain.AIDetectionMethod, previous, time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return nil, false, err
	}
	updated, err := json.Marshal(fields)
	return updated, err == nil, err
}

func safeSavedSourceName(name string) bool {
	return name != "" && name != "." && !strings.ContainsAny(name, "\\\x00:") &&
		!path.IsAbs(name) && path.Clean(name) == name && name != ".." && !strings.HasPrefix(name, "../")
}
