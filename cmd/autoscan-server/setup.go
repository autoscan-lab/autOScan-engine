package main

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var globalFiles = []string{"banned.yaml", "ai_dictionary.yaml"}

type setupResult struct {
	Assignment      string `json:"assignment"`
	FilesDownloaded int    `json:"files_downloaded"`
	ConfigDir       string `json:"config_dir"`
}

// setupAssignment downloads the global files + the assignment prefix from R2,
// validates that policy.yml is present, then atomically swaps the result into
// the active config dir.
func setupAssignment(ctx context.Context, cfg config, r2 *r2Client, assignment string) (*setupResult, error) {
	staging := filepath.Join(cfg.dataDir, ".staging-"+assignment)
	if err := os.RemoveAll(staging); err != nil {
		return nil, fmt.Errorf("clearing staging dir: %w", err)
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return nil, fmt.Errorf("creating staging dir: %w", err)
	}

	// Globals first so per-lab files can override them.
	globalKeys, err := downloadGlobals(ctx, r2, staging)
	if err != nil {
		os.RemoveAll(staging)
		return nil, err
	}

	prefix := "assignments/" + assignment + "/"
	keys, err := r2.downloadPrefix(ctx, prefix, staging)
	if err != nil {
		os.RemoveAll(staging)
		return nil, err
	}
	if len(keys) == 0 {
		os.RemoveAll(staging)
		return nil, &httpError{status: 404, msg: fmt.Sprintf("no files found for assignment %q", assignment)}
	}

	if _, err := os.Stat(filepath.Join(staging, policyFileName)); err != nil {
		os.RemoveAll(staging)
		return nil, &httpError{status: 400, msg: fmt.Sprintf("missing %s in assignment %q", policyFileName, assignment)}
	}

	if err := activateStaging(cfg, staging); err != nil {
		return nil, err
	}

	return &setupResult{
		Assignment:      assignment,
		FilesDownloaded: len(globalKeys) + len(keys),
		ConfigDir:       cfg.currentDir,
	}, nil
}

func downloadGlobals(ctx context.Context, r2 *r2Client, dest string) ([]string, error) {
	var downloaded []string
	for _, key := range globalFiles {
		ok, err := r2.downloadObject(ctx, key, filepath.Join(dest, key))
		if err != nil {
			return nil, err
		}
		if ok {
			downloaded = append(downloaded, key)
		}
	}
	return downloaded, nil
}

// activateStaging atomically swaps cfg.currentDir for staging, keeping the
// previous version as a backup that is restored if anything fails midway.
func activateStaging(cfg config, staging string) error {
	if err := os.MkdirAll(cfg.dataDir, 0o755); err != nil {
		return err
	}

	backup := filepath.Join(cfg.dataDir, ".previous")
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("clearing previous backup: %w", err)
	}

	currentExists := false
	if _, err := os.Stat(cfg.currentDir); err == nil {
		currentExists = true
		if err := os.Rename(cfg.currentDir, backup); err != nil {
			return fmt.Errorf("moving current to backup: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.Rename(staging, cfg.currentDir); err != nil {
		if currentExists {
			_ = os.Rename(backup, cfg.currentDir)
		}
		return fmt.Errorf("activating staging: %w", err)
	}

	if currentExists {
		_ = os.RemoveAll(backup)
	}
	return nil
}

func ensureActiveConfig(cfg config) error {
	if _, err := os.Stat(cfg.currentDir); err != nil {
		return &httpError{status: 503, msg: "no active assignment configured"}
	}
	if _, err := os.Stat(filepath.Join(cfg.currentDir, policyFileName)); err != nil {
		return &httpError{status: 503, msg: "active config is missing " + policyFileName}
	}
	return nil
}

// extractZip unpacks archivePath into targetDir, rejecting any entry whose
// resolved path escapes the target directory.
func extractZip(archivePath, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	rootAbs, err := filepath.Abs(targetDir)
	if err != nil {
		return err
	}

	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return &httpError{status: 400, msg: "uploaded file is not a valid zip archive"}
	}
	defer zr.Close()

	for _, f := range zr.File {
		dest, err := filepath.Abs(filepath.Join(targetDir, f.Name))
		if err != nil {
			return err
		}
		if dest != rootAbs && !strings.HasPrefix(dest, rootAbs+string(os.PathSeparator)) {
			return &httpError{status: 400, msg: "zip archive contains an invalid path"}
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, f.Mode()); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}

		if err := writeZipEntry(f, dest); err != nil {
			return err
		}
	}
	return nil
}

func writeZipEntry(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	mode := f.Mode()
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}
