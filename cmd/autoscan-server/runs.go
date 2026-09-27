package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
)

const (
	runsDirName     = "runs"
	runStateFile    = "run.json"
	runWorkspaceDir = "workspace"
	runConfigDir    = "config"
)

type runState struct {
	RunID       string              `json:"run_id"`
	CreatedAt   string              `json:"created_at"`
	SourceFile  string              `json:"source_file"`
	Submissions []domain.Submission `json:"submissions"`
}

func newRunID() (string, error) {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generating run id: %w", err)
	}
	return hex.EncodeToString(bytes[:]), nil
}

func runWorkspacePath(cfg config, runID string) (string, error) {
	base, err := runBasePath(cfg, runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, runWorkspaceDir), nil
}

// The run's own copy of its assignment config (policy, libraries, test files), as graded.
func runConfigPath(cfg config, runID string) (string, error) {
	base, err := runBasePath(cfg, runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, runConfigDir), nil
}

func runUploadPath(cfg config, runID string, extension string) (string, error) {
	base, err := runBasePath(cfg, runID)
	if err != nil {
		return "", err
	}
	suffix := strings.TrimSpace(extension)
	if suffix == "" {
		suffix = ".zip"
	}
	return filepath.Join(base, "upload"+suffix), nil
}

func saveRunState(cfg config, state runState) error {
	runID, err := normalizeRunID(state.RunID)
	if err != nil {
		return err
	}
	base, err := runBasePath(cfg, runID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}

	state.RunID = runID
	if state.CreatedAt == "" {
		state.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding run state: %w", err)
	}

	return os.WriteFile(filepath.Join(base, runStateFile), body, 0o644)
}

func loadRunState(cfg config, runID string) (runState, error) {
	normalizedRunID, err := normalizeRunID(runID)
	if err != nil {
		return runState{}, err
	}

	base, err := runBasePath(cfg, normalizedRunID)
	if err != nil {
		return runState{}, err
	}
	body, err := os.ReadFile(filepath.Join(base, runStateFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return runState{}, &httpError{status: 404, msg: "run not found"}
		}
		return runState{}, err
	}

	var state runState
	if err := json.Unmarshal(body, &state); err != nil {
		return runState{}, fmt.Errorf("decoding run state: %w", err)
	}
	if state.RunID == "" {
		state.RunID = normalizedRunID
	}
	return state, nil
}

func runBasePath(cfg config, runID string) (string, error) {
	normalizedRunID, err := normalizeRunID(runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg.dataDir, runsDirName, normalizedRunID), nil
}

// Runs are kept until the data disk passes this, then the oldest go first.
const diskHighWater = 0.90

// Never removes the newest run, so a just-graded run keeps its terminal.
func pruneOldRuns(cfg config) {
	runsDir := filepath.Join(cfg.dataDir, runsDirName)
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return
	}
	type storedRun struct {
		path     string
		modified time.Time
	}
	var stored []storedRun
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		stored = append(stored, storedRun{filepath.Join(runsDir, entry.Name()), info.ModTime()})
	}
	sort.Slice(stored, func(i, j int) bool { return stored[i].modified.Before(stored[j].modified) })

	for i := 0; i < len(stored)-1; i++ {
		used, ok := diskUsed(cfg.dataDir)
		if !ok || used < diskHighWater {
			return
		}
		log.Printf("data disk %.0f%% full, removing oldest run %s", used*100, filepath.Base(stored[i].path))
		_ = os.RemoveAll(stored[i].path)
	}
}

func diskUsed(dir string) (float64, bool) {
	var fs unix.Statfs_t
	if err := unix.Statfs(dir, &fs); err != nil || fs.Blocks == 0 {
		return 0, false
	}
	return float64(fs.Blocks-fs.Bfree) / float64(fs.Blocks), true
}

func normalizeRunID(runID string) (string, error) {
	normalized := strings.TrimSpace(runID)
	if normalized == "" {
		return "", &httpError{status: 400, msg: "missing run_id"}
	}
	for _, char := range normalized {
		isDigit := char >= '0' && char <= '9'
		isLower := char >= 'a' && char <= 'z'
		isUpper := char >= 'A' && char <= 'Z'
		if isDigit || isLower || isUpper || char == '-' || char == '_' {
			continue
		}
		return "", &httpError{status: 400, msg: "invalid run_id"}
	}
	return normalized, nil
}
