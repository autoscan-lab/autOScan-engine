package domain

import "time"

type SubmissionResult struct {
	Submission Submission       `json:"submission"`
	Compile    CompileResult    `json:"compile"`
	Scan       ScanResult       `json:"scan"`
	Status     SubmissionStatus `json:"status"`
}

type SubmissionStatus string

const (
	StatusPending  SubmissionStatus = "pending"
	StatusRunning  SubmissionStatus = "running"
	StatusClean    SubmissionStatus = "clean"
	StatusBanned   SubmissionStatus = "banned"
	StatusFailed   SubmissionStatus = "failed"
	StatusTimedOut SubmissionStatus = "timed_out"
	StatusError    SubmissionStatus = "error"
)

func NewSubmissionResult(sub Submission, compile CompileResult, scan ScanResult) SubmissionResult {
	status := StatusClean
	if compile.TimedOut {
		status = StatusTimedOut
	} else if !compile.OK {
		status = StatusFailed
	} else if scan.TotalHits() > 0 {
		status = StatusBanned
	}
	return SubmissionResult{Submission: sub, Compile: compile, Scan: scan, Status: status}
}

type RunReport struct {
	PolicyName string             `json:"policy_name"`
	Root       string             `json:"root"`
	StartedAt  time.Time          `json:"started_at"`
	FinishedAt time.Time          `json:"finished_at"`
	Results    []SubmissionResult `json:"results"`
	Summary    SummaryStats       `json:"summary"`
}

type SummaryStats struct {
	TotalSubmissions      int            `json:"total_submissions"`
	CompilePass           int            `json:"compile_pass"`
	CompileFail           int            `json:"compile_fail"`
	CompileTimeout        int            `json:"compile_timeout"`
	BannedHitsTotal       int            `json:"banned_hits_total"`
	SubmissionsWithBanned int            `json:"submissions_with_banned"`
	CleanSubmissions      int            `json:"clean_submissions"`
	TopBannedFunctions    map[string]int `json:"top_banned_functions,omitempty"`
	DurationMs            int64          `json:"duration_ms"`
}

func NewRunReport(policyName, root string, startedAt, finishedAt time.Time, results []SubmissionResult) RunReport {
	return RunReport{
		PolicyName: policyName, Root: root, StartedAt: startedAt, FinishedAt: finishedAt,
		Results: results, Summary: computeSummary(results, finishedAt.Sub(startedAt).Milliseconds()),
	}
}

func computeSummary(results []SubmissionResult, durationMs int64) SummaryStats {
	stats := SummaryStats{TotalSubmissions: len(results), TopBannedFunctions: make(map[string]int), DurationMs: durationMs}

	for _, r := range results {
		switch {
		case r.Compile.TimedOut:
			stats.CompileTimeout++
		case !r.Compile.OK:
			stats.CompileFail++
		default:
			stats.CompilePass++
		}

		if r.Scan.TotalHits() > 0 {
			stats.SubmissionsWithBanned++
			stats.BannedHitsTotal += r.Scan.TotalHits()
			for fn, hits := range r.Scan.HitsByFunction {
				stats.TopBannedFunctions[fn] += len(hits)
			}
		}

		if r.Status == StatusClean {
			stats.CleanSubmissions++
		}
	}
	return stats
}
