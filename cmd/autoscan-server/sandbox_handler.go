package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	aipkg "github.com/autoscan-lab/autoscan-engine/pkg/ai"
	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/engine"
)

const sandboxSourceFile = "__sandbox.c"

var submissionArchiveExts = []string{".tar.gz", ".tgz", ".tar", ".zip"}

type sandboxSummary struct {
	SubmissionCount    int `json:"submission_count"`
	PairCount          int `json:"pair_count"`
	FlaggedPairs       int `json:"flagged_pairs"`
	FlaggedSubmissions int `json:"flagged_submissions"`
}

type sandboxFile struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type sandboxSubmissionSource struct {
	ID     string        `json:"id"`
	Source string        `json:"source"`
	Files  []sandboxFile `json:"files"`
}

type sandboxAnalyzeResponse struct {
	Similarity  *domain.SimilarityReport  `json:"similarity,omitempty"`
	AIDetection *domain.AIDetectionReport `json:"ai_detection,omitempty"`
	Submissions []sandboxSubmissionSource `json:"submissions"`
	Summary     sandboxSummary            `json:"summary"`
}

func (s *server) sandboxAnalyze(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, &httpError{status: 400, msg: "invalid multipart form: " + err.Error()})
		return
	}
	r2Key := strings.TrimSpace(r.FormValue("r2_key"))
	if r2Key == "" {
		writeError(w, &httpError{status: 400, msg: "missing 'r2_key' field"})
		return
	}

	progress := s.progressReporterFor(r)
	defer progress.done()
	progress.report(0.03, "Downloading submissions")

	workDir, err := os.MkdirTemp(s.cfg.dataDir, "sandbox-")
	if err != nil {
		writeError(w, err)
		return
	}
	defer os.RemoveAll(workDir)

	r2, err := newR2Client(r.Context(), s.cfg)
	if err != nil {
		writeError(w, err)
		return
	}
	archivePath := filepath.Join(workDir, "upload"+filepath.Ext(r2Key))
	found, err := r2.downloadObject(r.Context(), r2Key, archivePath)
	if err != nil {
		writeError(w, err)
		return
	}
	if !found {
		writeError(w, &httpError{status: 404, msg: "r2_key not found: " + r2Key})
		return
	}

	progress.report(0.1, "Extracting submissions")
	outerDir := filepath.Join(workDir, "outer")
	if err := extractArchive(archivePath, outerDir); err != nil {
		writeError(w, err)
		return
	}

	progress.report(0.15, "Reading submissions")
	submissions, sources, err := discoverSandboxSubmissions(outerDir, filepath.Join(workDir, "subs"))
	if err != nil {
		writeError(w, err)
		return
	}
	if len(submissions) == 0 {
		writeError(w, &httpError{status: 400, msg: "no submissions found; expected a zip of per-submission .zip/.tar archives containing .c files"})
		return
	}

	progress.report(0.3, "Loading AI dictionary")
	dict, err := sandboxAIDictionary(r.Context(), s.cfg, workDir)
	if err != nil {
		writeError(w, err)
		return
	}

	progress.report(0.35, "Fingerprinting submissions")
	prints := engine.FingerprintSubmissions(submissions, sandboxSourceFile, defaultCompareConfig)

	progress.report(0.55, "Computing similarity")
	sim, err := engine.ComputeSimilarityFromFingerprints(submissions, prints, sandboxSourceFile, defaultCompareConfig)
	if err != nil {
		writeError(w, &httpError{status: 500, msg: "computing similarity: " + err.Error()})
		return
	}
	trimSimilarityReport(&sim, true, 0)

	progress.report(0.8, "Running AI detection")
	ai, err := engine.ComputeAIDetectionFromFingerprints(submissions, prints, sandboxSourceFile, dict, defaultCompareConfig)
	if err != nil {
		writeError(w, &httpError{status: 500, msg: "computing ai detection: " + err.Error()})
		return
	}
	trimAIDetectionReport(&ai, true, 0)

	log.Printf("processed sandbox analyze with %d submissions", len(submissions))
	writeJSON(w, http.StatusOK, sandboxAnalyzeResponse{
		Similarity:  &sim,
		AIDetection: &ai,
		Submissions: sources,
		Summary: sandboxSummary{
			SubmissionCount:    len(submissions),
			PairCount:          countSimilarityPairs(&sim),
			FlaggedPairs:       countFlaggedSimilarityPairs(&sim),
			FlaggedSubmissions: countFlaggedAISubmissions(&ai),
		},
	})
}

func sandboxAIDictionary(ctx context.Context, cfg config, workDir string) (*aipkg.Dictionary, error) {
	r2, err := newR2Client(ctx, cfg)
	if err != nil {
		return nil, err
	}
	dictPath := filepath.Join(workDir, "ai_dictionary.yaml")
	ok, err := r2.downloadObject(ctx, "ai_dictionary.yaml", dictPath)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &httpError{status: 503, msg: "no ai_dictionary.yaml in R2; add patterns under Policies → AI dictionary"}
	}
	return aipkg.LoadDictionary(dictPath)
}

func extractArchive(archivePath, dest string) error {
	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(archivePath, dest)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTar(archivePath, dest, true)
	case strings.HasSuffix(lower, ".tar"):
		return extractTar(archivePath, dest, false)
	default:
		return &httpError{status: 400, msg: "unsupported archive type: " + filepath.Base(archivePath)}
	}
}

func extractTar(archivePath, dest string, gz bool) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	var reader io.Reader = f
	if gz {
		gzr, err := gzip.NewReader(f)
		if err != nil {
			return &httpError{status: 400, msg: "invalid gzip archive"}
		}
		defer gzr.Close()
		reader = gzr
	}

	tr := tar.NewReader(reader)
	budget := int64(maxZipBytes)
	entries := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return &httpError{status: 400, msg: "invalid tar archive"}
		}
		entries++
		if entries > maxZipEntries {
			return &httpError{status: 400, msg: "archive has too many entries"}
		}
		target, err := filepath.Abs(filepath.Join(dest, hdr.Name))
		if err != nil {
			return err
		}
		if target != rootAbs && !strings.HasPrefix(target, rootAbs+string(os.PathSeparator)) {
			return &httpError{status: 400, msg: "archive contains an invalid path"}
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			n, err := io.Copy(out, io.LimitReader(tr, budget+1))
			out.Close()
			if err != nil {
				return err
			}
			if n > budget {
				return &httpError{status: 400, msg: "archive is too large when decompressed"}
			}
			budget -= n
		}
	}
	return nil
}

func discoverSandboxSubmissions(srcDir, destDir string) ([]domain.Submission, []sandboxSubmissionSource, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, nil, err
	}
	var submissions []domain.Submission
	sourceByID := map[string]string{}
	filesByID := map[string][]sandboxFile{}
	seen := map[string]int{}

	err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !isSubmissionArchive(path) {
			return nil
		}
		id := submissionID(path)
		if rel, relErr := filepath.Rel(srcDir, filepath.Dir(path)); relErr == nil &&
			rel != "." && rel != "" {
			id = filepath.ToSlash(rel)
		}
		seen[id]++
		if seen[id] > 1 {
			id = fmt.Sprintf("%s-%d", id, seen[id])
		}
		subDir := filepath.Join(destDir, id)
		if err := extractArchive(path, subDir); err != nil {
			return err
		}
		combined, files, err := concatCSources(subDir)
		if err != nil {
			return err
		}
		if combined == "" {
			return nil
		}
		if err := os.WriteFile(filepath.Join(subDir, sandboxSourceFile), []byte(combined), 0o644); err != nil {
			return err
		}
		submissions = append(submissions, domain.NewSubmission(id, subDir, []string{sandboxSourceFile}))
		sourceByID[id] = combined
		filesByID[id] = files
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(submissions, func(i, j int) bool { return submissions[i].ID < submissions[j].ID })

	sources := make([]sandboxSubmissionSource, 0, len(submissions))
	for _, sub := range submissions {
		sources = append(sources, sandboxSubmissionSource{
			ID:     sub.ID,
			Source: sourceByID[sub.ID],
			Files:  filesByID[sub.ID],
		})
	}
	return submissions, sources, nil
}

func isSubmissionArchive(path string) bool {
	lower := strings.ToLower(path)
	for _, ext := range submissionArchiveExts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func submissionID(path string) string {
	name := filepath.Base(path)
	lower := strings.ToLower(name)
	for _, ext := range submissionArchiveExts {
		if strings.HasSuffix(lower, ext) {
			return name[:len(name)-len(ext)]
		}
	}
	return name
}

// The manifest's 1-based line ranges use the same coordinates the analyzers report span lines in.
func concatCSources(dir string) (string, []sandboxFile, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		lower := strings.ToLower(path)
		if strings.HasSuffix(lower, ".c") || strings.HasSuffix(lower, ".h") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	sort.Strings(files)

	var b strings.Builder
	manifest := make([]sandboxFile, 0, len(files))
	line := 1
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return "", nil, err
		}
		rel, _ := filepath.Rel(dir, f)
		rel = filepath.ToSlash(rel)

		b.WriteString("/* ==== " + rel + " ==== */\n")
		line++

		src := string(data)
		contentLines := strings.Count(src, "\n")
		if len(src) == 0 || !strings.HasSuffix(src, "\n") {
			contentLines++
		}
		manifest = append(manifest, sandboxFile{
			Path:      rel,
			StartLine: line,
			EndLine:   line + contentLines - 1,
		})

		b.Write(data)
		b.WriteString("\n")
		line += strings.Count(src, "\n") + 1
	}
	return b.String(), manifest, nil
}
