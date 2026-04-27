package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
)

type analyzeRequest struct {
	RunID        string `json:"run_id"`
	IncludeSpans *bool  `json:"include_spans,omitempty"`
	TopK         int    `json:"top_k,omitempty"`
}

type similarityAnalysisSummary struct {
	FlaggedPairs int `json:"flagged_pairs"`
	PairCount    int `json:"pair_count"`
}

type similarityAnalysisResponse struct {
	RunID      string                    `json:"run_id"`
	Similarity *domain.SimilarityReport  `json:"similarity,omitempty"`
	Summary    similarityAnalysisSummary `json:"summary"`
}

type aiDetectionAnalysisSummary struct {
	FlaggedSubmissions int `json:"flagged_submissions"`
	SubmissionCount    int `json:"submission_count"`
}

type aiDetectionAnalysisResponse struct {
	AIDetection *domain.AIDetectionReport  `json:"ai_detection,omitempty"`
	RunID       string                     `json:"run_id"`
	Summary     aiDetectionAnalysisSummary `json:"summary"`
}

func (s *server) analyzeSimilarity(w http.ResponseWriter, r *http.Request) {
	request, err := parseAnalyzeRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}

	state, err := loadRunState(s.cfg, request.RunID)
	if err != nil {
		writeError(w, err)
		return
	}

	similarity, _, err := runAnalysis(s.cfg, state.SourceFile, state.Submissions, analysisOptions{
		IncludeSimilarity:      true,
		SimilarityIncludeSpans: request.includeSpans(),
		SimilarityTopK:         request.TopK,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	response := similarityAnalysisResponse{
		RunID:      state.RunID,
		Similarity: similarity,
		Summary: similarityAnalysisSummary{
			FlaggedPairs: countFlaggedSimilarityPairs(similarity),
			PairCount:    countSimilarityPairs(similarity),
		},
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) analyzeAIDetection(w http.ResponseWriter, r *http.Request) {
	request, err := parseAnalyzeRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}

	state, err := loadRunState(s.cfg, request.RunID)
	if err != nil {
		writeError(w, err)
		return
	}

	_, detection, err := runAnalysis(s.cfg, state.SourceFile, state.Submissions, analysisOptions{
		IncludeAIDetection:      true,
		AIDetectionIncludeSpans: request.includeSpans(),
		AIDetectionTopK:         request.TopK,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	response := aiDetectionAnalysisResponse{
		AIDetection: detection,
		RunID:       state.RunID,
		Summary: aiDetectionAnalysisSummary{
			FlaggedSubmissions: countFlaggedAISubmissions(detection),
			SubmissionCount:    countAISubmissions(detection),
		},
	}
	writeJSON(w, http.StatusOK, response)
}

func parseAnalyzeRequest(r *http.Request) (analyzeRequest, error) {
	var request analyzeRequest

	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "application/json") {
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			return analyzeRequest{}, &httpError{status: 400, msg: "invalid JSON body: " + err.Error()}
		}
	} else {
		if err := r.ParseForm(); err != nil {
			return analyzeRequest{}, &httpError{status: 400, msg: "invalid form body: " + err.Error()}
		}
		request.RunID = strings.TrimSpace(r.FormValue("run_id"))
		if value := strings.TrimSpace(r.FormValue("include_spans")); value != "" {
			parsed, err := parseBoolString(value)
			if err != nil {
				return analyzeRequest{}, err
			}
			request.IncludeSpans = &parsed
		}
		if value := strings.TrimSpace(r.FormValue("top_k")); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return analyzeRequest{}, &httpError{status: 400, msg: "top_k must be an integer"}
			}
			request.TopK = parsed
		}
	}

	request.RunID = strings.TrimSpace(request.RunID)
	if request.RunID == "" {
		return analyzeRequest{}, &httpError{status: 400, msg: "missing run_id"}
	}
	if request.TopK < 0 {
		return analyzeRequest{}, &httpError{status: 400, msg: "top_k must be >= 0"}
	}
	return request, nil
}

func (request analyzeRequest) includeSpans() bool {
	return request.IncludeSpans != nil && *request.IncludeSpans
}

func parseBoolString(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, &httpError{status: 400, msg: fmt.Sprintf("invalid boolean value: %q", value)}
	}
}

func countSimilarityPairs(report *domain.SimilarityReport) int {
	if report == nil {
		return 0
	}
	return len(report.Pairs)
}

func countFlaggedSimilarityPairs(report *domain.SimilarityReport) int {
	if report == nil {
		return 0
	}
	flagged := 0
	for _, pair := range report.Pairs {
		if pair.Flagged {
			flagged++
		}
	}
	return flagged
}

func countAISubmissions(report *domain.AIDetectionReport) int {
	if report == nil {
		return 0
	}
	return len(report.Submissions)
}

func countFlaggedAISubmissions(report *domain.AIDetectionReport) int {
	if report == nil {
		return 0
	}
	flagged := 0
	for _, submission := range report.Submissions {
		if submission.Flagged {
			flagged++
		}
	}
	return flagged
}
