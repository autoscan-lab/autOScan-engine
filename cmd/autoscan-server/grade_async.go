package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
)

const asyncGradeTimeout = 30 * time.Minute

// Written to R2 at <result_key_prefix>/<run_id>/result.json for the caller to collect.
type asyncGradeResult struct {
	*gradeResponse
	Similarity  *domain.SimilarityReport  `json:"similarity,omitempty"`
	AIDetection *domain.AIDetectionReport `json:"ai_detection,omitempty"`
}

func (s *server) gradeAsync(w http.ResponseWriter, r *http.Request) {
	assignment := strings.TrimSpace(r.FormValue("assignment"))
	if assignment == "" || strings.ContainsAny(assignment, "/\\") {
		writeError(w, &httpError{status: 400, msg: "invalid assignment name"})
		return
	}
	r2Key := strings.TrimSpace(r.FormValue("r2_key"))
	if r2Key == "" {
		writeError(w, &httpError{status: 400, msg: "missing 'r2_key' field"})
		return
	}
	resultPrefix := strings.TrimSpace(r.FormValue("result_key_prefix"))
	if resultPrefix == "" {
		writeError(w, &httpError{status: 400, msg: "missing 'result_key_prefix' field"})
		return
	}
	exportPrefix := strings.TrimSpace(r.FormValue("export_key_prefix"))

	runID, err := newRunID()
	if err != nil {
		writeError(w, err)
		return
	}

	s.progress.set(runID, 0.01, "Queued")
	go s.runGradeJob(runID, assignment, r2Key, exportPrefix, resultPrefix)

	writeJSON(w, http.StatusAccepted, map[string]string{"run_id": runID})
}

func (s *server) runGradeJob(runID, assignment, r2Key, exportPrefix, resultPrefix string) {
	ctx, cancel := context.WithTimeout(context.Background(), asyncGradeTimeout)
	defer cancel()

	if err := s.executeGradeJob(ctx, runID, assignment, r2Key, exportPrefix, resultPrefix); err != nil {
		log.Printf("async grade run_id=%s failed: %v", runID, err)
		s.progress.finish(runID, false, publicJobError(err))
		return
	}
	s.progress.finish(runID, true, "")
}

func (s *server) executeGradeJob(ctx context.Context, runID, assignment, r2Key, exportPrefix, resultPrefix string) error {
	progress := progressReporter{tracker: s.progress, token: runID}

	r2, err := newR2Client(ctx, s.cfg)
	if err != nil {
		return err
	}

	progress.report(0.01, "Preparing assignment")
	s.mu.Lock()
	_, err = setupAssignment(ctx, s.cfg, r2, assignment)
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("setting up assignment: %w", err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := ensureActiveConfig(s.cfg); err != nil {
		return err
	}

	runBase, err := runBasePath(s.cfg, runID)
	if err != nil {
		return err
	}
	cleanupRun := true
	defer func() {
		if cleanupRun {
			_ = os.RemoveAll(runBase)
		}
	}()
	if err := os.MkdirAll(runBase, 0o755); err != nil {
		return err
	}

	archivePath, err := runUploadPath(s.cfg, runID, filepath.Ext(r2Key))
	if err != nil {
		return err
	}
	workspaceDir, err := runWorkspacePath(s.cfg, runID)
	if err != nil {
		return err
	}

	progress.report(0.02, "Downloading submissions")
	found, err := r2.downloadObject(ctx, r2Key, archivePath)
	if err != nil {
		return err
	}
	if !found {
		return &httpError{status: 404, msg: "r2_key not found: " + r2Key}
	}

	progress.report(0.05, "Extracting submissions")
	if err := extractZip(archivePath, workspaceDir); err != nil {
		return err
	}
	_ = os.Remove(archivePath)

	exportKey := ""
	if exportPrefix != "" {
		exportKey = strings.TrimRight(exportPrefix, "/") + "/" + runID + "/export.zip"
	}
	resp, err := runGradingPipeline(ctx, s.cfg, workspaceDir, exportKey, progress)
	if err != nil {
		return err
	}
	resp.RunID = runID

	submissions := make([]domain.Submission, len(resp.Results))
	for index, result := range resp.Results {
		submissions[index] = result.Submission
	}
	if err := saveRunState(s.cfg, runState{
		RunID:       runID,
		SourceFile:  resp.SourceFile,
		Submissions: submissions,
	}); err != nil {
		return err
	}

	// A failed analysis must not fail the graded run.
	progress.report(0.99, "Analyzing submissions")
	result := asyncGradeResult{gradeResponse: resp}
	if similarity, _, err := runAnalysis(s.cfg, resp.SourceFile, submissions, analysisOptions{
		IncludeSimilarity:      true,
		SimilarityIncludeSpans: true,
		SimilarityMinScore:     10,
	}); err != nil {
		log.Printf("async grade run_id=%s similarity analysis failed: %v", runID, err)
	} else {
		result.Similarity = similarity
	}
	if _, detection, err := runAnalysis(s.cfg, resp.SourceFile, submissions, analysisOptions{
		IncludeAIDetection:      true,
		AIDetectionIncludeSpans: true,
	}); err != nil {
		log.Printf("async grade run_id=%s ai detection failed: %v", runID, err)
	} else {
		result.AIDetection = detection
	}

	progress.report(0.995, "Saving results")
	resultKey := strings.TrimRight(resultPrefix, "/") + "/" + runID + "/result.json"
	if err := uploadJSON(ctx, r2, resultKey, result); err != nil {
		return fmt.Errorf("uploading result: %w", err)
	}

	cleanupRun = false
	pruneOldRuns(s.cfg)

	log.Printf("processed async grading run_id=%s with %d submissions", runID, len(resp.Results))
	return nil
}

func uploadJSON(ctx context.Context, r2 *r2Client, key string, payload any) error {
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "autoscan-result-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return r2.uploadObject(ctx, key, tmpPath, "application/json")
}

func publicJobError(err error) string {
	var he *httpError
	if errors.As(err, &he) {
		return he.msg
	}
	return "grading failed"
}
