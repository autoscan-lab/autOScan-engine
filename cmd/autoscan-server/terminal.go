package main

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/coder/websocket"

	"github.com/autoscan-lab/autoscan-engine/internal/terminal"
	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
)

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func copyFilesNoClobber(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		target := filepath.Join(dst, entry.Name())
		if _, err := os.Stat(target); err == nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, entry.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func buildTerminalScratch(configDir string, sub domain.Submission) (string, error) {
	scratch, err := os.MkdirTemp("", "autoscan-term-*")
	if err != nil {
		return "", err
	}
	if err := copyTree(sub.Path, scratch); err != nil {
		_ = os.RemoveAll(scratch)
		return "", err
	}
	for _, dir := range []string{"libraries", "test_files"} {
		if err := copyFilesNoClobber(filepath.Join(configDir, dir), scratch); err != nil {
			_ = os.RemoveAll(scratch)
			return "", err
		}
	}
	return scratch, nil
}

// Runs graded before per-run configs existed have none, so fetch the assignment the token names.
func (s *server) runConfig(ctx context.Context, claims terminal.Claims) (string, error) {
	configDir, err := runConfigPath(s.cfg, claims.RunID)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(configDir); err == nil {
		return configDir, nil
	}
	if !validAssignmentName(claims.Assignment) {
		return "", &httpError{status: 410, msg: "run has no assignment config; re-run grading"}
	}
	r2, err := newR2Client(ctx, s.cfg)
	if err != nil {
		return "", err
	}
	if err := fetchAssignmentConfig(ctx, r2, claims.Assignment, configDir); err != nil {
		return "", err
	}
	return configDir, nil
}

func (s *server) terminal(w http.ResponseWriter, r *http.Request) {
	claims, err := terminal.ParseToken(s.cfg.engineSecret, r.URL.Query().Get("token"))
	if err != nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	state, err := loadRunState(s.cfg, claims.RunID)
	if err != nil {
		writeError(w, err)
		return
	}
	var sub *domain.Submission
	for index := range state.Submissions {
		if state.Submissions[index].ID == claims.SubmissionID {
			sub = &state.Submissions[index]
			break
		}
	}
	if sub == nil {
		writeError(w, &httpError{status: 404, msg: "submission not found in run"})
		return
	}
	workspaceDir, err := runWorkspacePath(s.cfg, claims.RunID)
	if err != nil {
		writeError(w, err)
		return
	}
	if rel, err := filepath.Rel(workspaceDir, sub.Path); err != nil || strings.HasPrefix(rel, "..") {
		writeError(w, &httpError{status: 404, msg: "submission workspace not available"})
		return
	}
	if _, err := os.Stat(sub.Path); err != nil {
		writeError(w, &httpError{status: 410, msg: "submission workspace expired; re-run grading"})
		return
	}
	configDir, err := s.runConfig(r.Context(), claims)
	if err != nil {
		writeError(w, err)
		return
	}

	// Token-gated only; the short-lived signed token is the credential, so no origin check.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}

	ts, err := terminal.Join(claims, func() (string, error) {
		return buildTerminalScratch(configDir, *sub)
	})
	if err != nil {
		code := websocket.StatusInternalError
		if errors.Is(err, terminal.ErrSessionLimit) || errors.Is(err, terminal.ErrPaneLimit) || errors.Is(err, terminal.ErrSessionClosed) {
			code = websocket.StatusTryAgainLater
		}
		_ = conn.Close(code, err.Error())
		return
	}

	log.Printf("terminal: pane start run_id=%s submission=%s session=%s", claims.RunID, claims.SubmissionID, ts.ID())
	terminal.ServePane(conn, ts)
	log.Printf("terminal: pane end run_id=%s submission=%s session=%s", claims.RunID, claims.SubmissionID, ts.ID())
}
