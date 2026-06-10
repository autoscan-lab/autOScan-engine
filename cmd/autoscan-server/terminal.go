package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/engine"
)

// Interactive terminal: a PTY running bash inside the grading sandbox, scoped
// to a scratch copy of one graded submission, streamed over a WebSocket.
//
// Auth: the agent mints a short-lived HMAC token (signed with ENGINE_SECRET)
// binding {run_id, submission_id, exp}; the browser connects directly with it.
// Sessions run on a scratch copy so the graded workspace — which /analyze/*
// re-reads — is never mutated.

const (
	maxTerminalSessions = 5
	terminalIdleTimeout = 10 * time.Minute
	terminalMaxLifetime = 45 * time.Minute
)

var terminalSlots struct {
	sync.Mutex
	count int
}

func acquireTerminalSlot() bool {
	terminalSlots.Lock()
	defer terminalSlots.Unlock()
	if terminalSlots.count >= maxTerminalSessions {
		return false
	}
	terminalSlots.count++
	return true
}

func releaseTerminalSlot() {
	terminalSlots.Lock()
	defer terminalSlots.Unlock()
	terminalSlots.count--
}

type terminalClaims struct {
	RunID        string `json:"run_id"`
	SubmissionID string `json:"submission_id"`
	// Display label for the shell prompt (autoscan@<student>).
	Student string `json:"student,omitempty"`
	Exp     int64  `json:"exp"`
}

// parseTerminalToken validates "payload.sig" where payload is base64url JSON
// claims and sig is base64url HMAC-SHA256(secret, payload).
func parseTerminalToken(secret, token string) (terminalClaims, error) {
	payload, sig, ok := strings.Cut(token, ".")
	if !ok || payload == "" || sig == "" {
		return terminalClaims{}, errors.New("malformed token")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(want), []byte(sig)) != 1 {
		return terminalClaims{}, errors.New("invalid token signature")
	}

	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return terminalClaims{}, errors.New("malformed token payload")
	}
	var claims terminalClaims
	if err := json.Unmarshal(body, &claims); err != nil {
		return terminalClaims{}, errors.New("malformed token claims")
	}
	if claims.RunID == "" || claims.SubmissionID == "" {
		return terminalClaims{}, errors.New("incomplete token claims")
	}
	if time.Now().Unix() > claims.Exp {
		return terminalClaims{}, errors.New("token expired")
	}
	return claims, nil
}

// promptLabel reduces a student display name to a safe prompt token.
func promptLabel(student string) string {
	var out []rune
	for _, r := range strings.ToLower(strings.TrimSpace(student)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			out = append(out, r)
		case r == ' ':
			out = append(out, '-')
		}
		if len(out) >= 32 {
			break
		}
	}
	if len(out) == 0 {
		return "submission"
	}
	return string(out)
}

func terminalEnv(homeDir, student string) []string {
	return []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + homeDir,
		"LANG=C",
		"LC_ALL=C",
		"TERM=xterm-256color",
		// Bash keeps an inherited PS1 (no rc files exist in the sandbox).
		`PS1=autoscan@` + promptLabel(student) + `:\w$ `,
	}
}

// copyTree copies regular files and directories from src into dst.
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

// copyFilesNoClobber copies src's regular files flat into dst, skipping names
// that already exist (student files win over policy companions).
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

// buildTerminalScratch copies the submission plus the active policy's library
// and test files into a fresh temp dir the session can freely mutate.
func buildTerminalScratch(cfg config, sub domain.Submission) (string, error) {
	scratch, err := os.MkdirTemp("", "autoscan-term-*")
	if err != nil {
		return "", err
	}
	if err := copyTree(sub.Path, scratch); err != nil {
		_ = os.RemoveAll(scratch)
		return "", err
	}
	for _, dir := range []string{"libraries", "test_files"} {
		if err := copyFilesNoClobber(filepath.Join(cfg.currentDir, dir), scratch); err != nil {
			_ = os.RemoveAll(scratch)
			return "", err
		}
	}
	return scratch, nil
}

func (s *server) terminal(w http.ResponseWriter, r *http.Request) {
	claims, err := parseTerminalToken(s.cfg.engineSecret, r.URL.Query().Get("token"))
	if err != nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Resolve the submission before upgrading so failures are plain HTTP.
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

	// Token-gated only; the short-lived signed token is the credential.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}

	if !acquireTerminalSlot() {
		_ = conn.Close(websocket.StatusTryAgainLater, "terminal session limit reached")
		return
	}

	s.mu.RLock()
	scratch, err := buildTerminalScratch(s.cfg, *sub)
	s.mu.RUnlock()
	if err != nil {
		releaseTerminalSlot()
		log.Printf("terminal: scratch setup failed run_id=%s submission=%s: %v", claims.RunID, claims.SubmissionID, err)
		_ = conn.Close(websocket.StatusInternalError, "could not prepare workspace")
		return
	}

	log.Printf("terminal: session start run_id=%s submission=%s", claims.RunID, claims.SubmissionID)
	runTerminalSession(conn, scratch, claims.Student)
	log.Printf("terminal: session end run_id=%s submission=%s", claims.RunID, claims.SubmissionID)
}

func runTerminalSession(conn *websocket.Conn, scratch, student string) {
	defer releaseTerminalSlot()
	defer os.RemoveAll(scratch)

	argv, cgroupCleanup, _ := engine.InteractiveSandbox(scratch, []string{"bash"})
	defer cgroupCleanup()

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = scratch
	cmd.Env = terminalEnv(scratch, student)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		log.Printf("terminal: shell start failed: %v", err)
		_ = conn.Close(websocket.StatusInternalError, "could not start shell")
		return
	}
	defer func() { _ = cmd.Wait() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Cols: 80, Rows: 24})

	ctx, cancel := context.WithTimeout(context.Background(), terminalMaxLifetime)
	defer cancel()

	var once sync.Once
	finish := func(code websocket.StatusCode, reason string) {
		once.Do(func() {
			if cmd.Process != nil {
				// pty.Start makes the shell a session leader, so its pid is
				// the process group covering everything it spawned.
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			_ = ptmx.Close()
			_ = conn.Close(code, reason)
			cancel()
		})
	}

	idle := time.AfterFunc(terminalIdleTimeout, func() {
		finish(websocket.StatusNormalClosure, "session closed after inactivity")
	})
	defer idle.Stop()

	go func() {
		<-ctx.Done()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			finish(websocket.StatusNormalClosure, "session time limit reached")
		}
	}()

	// Shell output → socket.
	go func() {
		buf := make([]byte, 16<<10)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				if conn.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
					finish(websocket.StatusNormalClosure, "connection closed")
					return
				}
			}
			if readErr != nil {
				finish(websocket.StatusNormalClosure, "shell exited")
				return
			}
		}
	}()

	// Keepalive through proxies; also detects vanished clients.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if conn.Ping(ctx) != nil {
					finish(websocket.StatusNormalClosure, "connection lost")
					return
				}
			}
		}
	}()

	// Socket → shell. Binary frames are raw input; text frames are control
	// messages (resize). Only real input resets the idle timer.
	conn.SetReadLimit(1 << 20)
	for {
		kind, data, readErr := conn.Read(ctx)
		if readErr != nil {
			finish(websocket.StatusNormalClosure, "connection closed")
			return
		}
		switch kind {
		case websocket.MessageBinary:
			idle.Reset(terminalIdleTimeout)
			if _, writeErr := ptmx.Write(data); writeErr != nil {
				finish(websocket.StatusNormalClosure, "shell exited")
				return
			}
		case websocket.MessageText:
			var msg struct {
				Type string `json:"type"`
				Cols uint16 `json:"cols"`
				Rows uint16 `json:"rows"`
			}
			if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
				_ = pty.Setsize(ptmx, &pty.Winsize{Cols: msg.Cols, Rows: msg.Rows})
			}
		}
	}
}
