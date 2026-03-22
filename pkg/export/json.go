package export

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
)

type JSONReport struct {
	PolicyName string           `json:"policy_name"`
	Root       string           `json:"root"`
	StartedAt  time.Time        `json:"started_at"`
	FinishedAt time.Time        `json:"finished_at"`
	DurationMs int64            `json:"duration_ms"`
	Summary    JSONSummary      `json:"summary"`
	Results    []JSONSubmission `json:"results"`
}

type JSONSummary struct {
	TotalSubmissions      int            `json:"total_submissions"`
	CompilePass           int            `json:"compile_pass"`
	CompileFail           int            `json:"compile_fail"`
	CompileTimeout        int            `json:"compile_timeout"`
	CleanSubmissions      int            `json:"clean_submissions"`
	SubmissionsWithBanned int            `json:"submissions_with_banned"`
	BannedHitsTotal       int            `json:"banned_hits_total"`
	TopBannedFunctions    map[string]int `json:"top_banned_functions"`
}

type JSONSubmission struct {
	ID             string          `json:"id"`
	Status         string          `json:"status"`
	CFiles         []string        `json:"c_files"`
	CompileOK      bool            `json:"compile_ok"`
	CompileTimeout bool            `json:"compile_timeout"`
	ExitCode       int             `json:"exit_code"`
	CompileTimeMs  int64           `json:"compile_time_ms"`
	Stderr         string          `json:"stderr,omitempty"`
	BannedCount    int             `json:"banned_count"`
	BannedHits     []JSONBannedHit `json:"banned_hits,omitempty"`
}

type JSONBannedHit struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Snippet  string `json:"snippet"`
}

// Export a report to JSON format
func JSON(report domain.RunReport, outputDir string) (string, error) {
	jr := JSONReport{
		PolicyName: report.PolicyName,
		Root:       report.Root,
		StartedAt:  report.StartedAt,
		FinishedAt: report.FinishedAt,
		DurationMs: report.Summary.DurationMs,
		Summary: JSONSummary{
			TotalSubmissions:      report.Summary.TotalSubmissions,
			CompilePass:           report.Summary.CompilePass,
			CompileFail:           report.Summary.CompileFail,
			CompileTimeout:        report.Summary.CompileTimeout,
			CleanSubmissions:      report.Summary.CleanSubmissions,
			SubmissionsWithBanned: report.Summary.SubmissionsWithBanned,
			BannedHitsTotal:       report.Summary.BannedHitsTotal,
			TopBannedFunctions:    report.Summary.TopBannedFunctions,
		},
		Results: make([]JSONSubmission, len(report.Results)),
	}

	for i, r := range report.Results {
		js := JSONSubmission{
			ID:             r.Submission.ID,
			Status:         string(r.Status),
			CFiles:         r.Submission.CFiles,
			CompileOK:      r.Compile.OK,
			CompileTimeout: r.Compile.TimedOut,
			ExitCode:       r.Compile.ExitCode,
			CompileTimeMs:  r.Compile.DurationMs,
			BannedCount:    r.Scan.TotalHits(),
		}

		if !r.Compile.OK && r.Compile.Stderr != "" {
			js.Stderr = r.Compile.Stderr
		}

		if len(r.Scan.Hits) > 0 {
			js.BannedHits = make([]JSONBannedHit, len(r.Scan.Hits))
			for j, hit := range r.Scan.Hits {
				js.BannedHits[j] = JSONBannedHit{
					Function: hit.Function,
					File:     hit.File,
					Line:     hit.Line,
					Column:   hit.Column,
					Snippet:  hit.Snippet,
				}
			}
		}

		jr.Results[i] = js
	}

	data, err := json.MarshalIndent(jr, "", "  ")
	if err != nil {
		return "", err
	}

	filename := filepath.Join(outputDir, "report.json")
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return "", err
	}

	return filename, nil
}
