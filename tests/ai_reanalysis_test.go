package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/engine"
)

func savedAIResult(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"run_id": "test-run", "source_file": "lab.c", "summary": map[string]int{"clean": 1},
		"feedback": map[string]any{"grade": 8.5, "comment": "Keep this exactly"},
		"results": []any{map[string]any{
			"submission":   domain.NewSubmission("student", "/unavailable/workspace", []string{"lab.c"}),
			"source_files": []any{map[string]string{"name": "lab.c", "content": assistantStyled}},
			"compile":      map[string]any{"ok": true}, "tests": map[string]int{"passed": 3},
		}},
		"similarity": map[string]any{"source_file": "lab.c", "pairs": []any{}},
		"ai_detection": map[string]any{
			"source_file": "lab.c", "dictionary_entry_count": 1, "dictionary_usable": 1,
			"submissions": []any{map[string]any{
				"id": "student", "best_score": 0.72, "match_count": 1,
				"ai_score": map[string]any{"score": 90, "method": "heuristic-v1"},
				"matches":  []any{map[string]any{"entry_id": "pattern", "title": "Preserved pattern", "score": 0.9}},
				"tells":    []any{map[string]any{"kind": "tool_file", "file": ".claude/settings.json", "label": "Tool config", "flagged": true}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestSavedAIReanalysisPreservesGradingDictionaryAndArtifacts(t *testing.T) {
	original := savedAIResult(t)
	updated, changed, err := engine.ReanalyzeSavedAIDetection(context.Background(), original, similarityConfig)
	if err != nil || !changed {
		t.Fatalf("refresh: %v changed=%v", err, changed)
	}
	var before, after map[string]json.RawMessage
	_ = json.Unmarshal(original, &before)
	_ = json.Unmarshal(updated, &after)
	for key, value := range before {
		if key != "ai_detection" && !bytes.Equal(value, after[key]) {
			t.Fatalf("changed unrelated field %s", key)
		}
	}
	var report domain.AIDetectionReport
	if err := json.Unmarshal(after["ai_detection"], &report); err != nil {
		t.Fatal(err)
	}
	row := report.Submissions[0]
	if report.Method != domain.AIDetectionMethod || row.AIScore == nil || row.AIScore.Method != domain.AIDetectionMethod || row.AIScore.Score < 72 {
		t.Fatalf("missing refreshed methodology or dictionary score: %+v", row)
	}
	if report.DictionaryUsable != 1 || row.MatchCount != 1 || row.Matches[0].EntryID != "pattern" {
		t.Fatal("lost dictionary provenance")
	}
	found := false
	for _, tell := range row.Tells {
		found = found || tell.Kind == "tool_file" && tell.File == ".claude/settings.json"
	}
	if !found {
		t.Fatal("lost archive artifact")
	}
	if next, changed, err := engine.ReanalyzeSavedAIDetection(context.Background(), updated, similarityConfig); err != nil || changed || !bytes.Equal(next, updated) {
		t.Fatalf("current methodology was not idempotent: %v changed=%v", err, changed)
	}
}

func TestSavedAIReanalysisRejectsIncompleteSourcesAndTraversal(t *testing.T) {
	for _, name := range []string{"../escape.c", "/absolute.c", "folder/../../escape.c", "a\\b.c", "lab.c"} {
		t.Run(name, func(t *testing.T) {
			var payload map[string]any
			_ = json.Unmarshal(savedAIResult(t), &payload)
			row := payload["results"].([]any)[0].(map[string]any)
			if name == "lab.c" {
				row["source_files"] = nil
			} else {
				row["source_files"].([]any)[0].(map[string]any)["name"] = name
			}
			body, _ := json.Marshal(payload)
			original := append([]byte(nil), body...)
			if _, changed, err := engine.ReanalyzeSavedAIDetection(context.Background(), body, similarityConfig); err == nil || changed {
				t.Fatal("accepted missing or unsafe sources")
			}
			if !bytes.Equal(body, original) {
				t.Fatal("modified input on failure")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, changed, err := engine.ReanalyzeSavedAIDetection(ctx, savedAIResult(t), similarityConfig); err == nil || changed {
		t.Fatal("ignored cancellation")
	}
}

func TestSavedAIReanalysisIncludesUnscorableSubmissionsAndAllPeerSources(t *testing.T) {
	var payload map[string]any
	_ = json.Unmarshal(savedAIResult(t), &payload)
	delete(payload, "ai_detection")
	rows := payload["results"].([]any)
	for i, sub := range diverseMetricSources(t, t.TempDir()) {
		content, err := domain.ReadSourceFile(sub.Path + "/lab.c")
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, map[string]any{"submission": domain.NewSubmission(sub.ID, "gone", []string{"lab.c"}), "source_files": []any{map[string]string{"name": "lab.c", "content": string(content)}}, "number": i})
	}
	rows = append(rows, map[string]any{"submission": domain.NewSubmission("empty", "gone", nil)})
	payload["results"] = rows
	body, _ := json.Marshal(payload)
	updated, _, err := engine.ReanalyzeSavedAIDetection(context.Background(), body, similarityConfig)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		AI      domain.AIDetectionReport `json:"ai_detection"`
		Results []json.RawMessage        `json:"results"`
	}
	_ = json.Unmarshal(updated, &result)
	if len(result.AI.Submissions) != len(rows) || len(result.Results) != len(rows) {
		t.Fatal("lost a submission")
	}
	for _, row := range result.AI.Submissions {
		if row.SubmissionID == "empty" {
			if row.AIScore != nil {
				t.Fatal("invented score for absent code")
			}
		} else if row.TokenMetrics == nil || row.TokenMetrics.Method != "peer-bigram-v2" {
			t.Fatalf("missing new metrics for %s", row.SubmissionID)
		}
	}
	var before, after map[string]json.RawMessage
	_ = json.Unmarshal(body, &before)
	_ = json.Unmarshal(updated, &after)
	if !bytes.Equal(before["results"], after["results"]) {
		t.Fatal("rewrote submission results")
	}
}
