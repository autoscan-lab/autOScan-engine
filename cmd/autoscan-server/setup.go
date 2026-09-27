package main

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var globalFiles = []string{"banned.yaml", "ai_dictionary.yaml"}

const (
	maxZipEntries = 10000
	maxZipBytes   = 1 << 30 // 1 GiB decompressed
)

func validAssignmentName(name string) bool {
	return name != "" && !strings.ContainsAny(name, "/\\")
}

// Downloads the globals and the assignment's R2 prefix into dest, which must not exist yet.
// Staging then renaming keeps dest all-or-nothing when two callers race to create it.
func fetchAssignmentConfig(ctx context.Context, r2 *r2Client, assignment, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(dest), ".config-*")
	if err != nil {
		return err
	}

	// Globals first so per-lab files can override them.
	if err := downloadGlobals(ctx, r2, staging); err != nil {
		os.RemoveAll(staging)
		return err
	}

	keys, err := r2.downloadPrefix(ctx, "assignments/"+assignment+"/", staging)
	if err != nil {
		os.RemoveAll(staging)
		return err
	}
	if len(keys) == 0 {
		os.RemoveAll(staging)
		return &httpError{status: 404, msg: fmt.Sprintf("no files found for assignment %q", assignment)}
	}
	if _, err := os.Stat(filepath.Join(staging, policyFileName)); err != nil {
		os.RemoveAll(staging)
		return &httpError{status: 400, msg: fmt.Sprintf("missing %s in assignment %q", policyFileName, assignment)}
	}

	if err := os.Rename(staging, dest); err != nil {
		os.RemoveAll(staging)
		if _, statErr := os.Stat(filepath.Join(dest, policyFileName)); statErr == nil {
			return nil
		}
		return fmt.Errorf("activating assignment config: %w", err)
	}
	return nil
}

func downloadGlobals(ctx context.Context, r2 *r2Client, dest string) error {
	for _, key := range globalFiles {
		if _, err := r2.downloadObject(ctx, key, filepath.Join(dest, key)); err != nil {
			return err
		}
	}
	return nil
}

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

	if len(zr.File) > maxZipEntries {
		return &httpError{status: 400, msg: "zip archive has too many entries"}
	}
	budget := int64(maxZipBytes)

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

		if err := writeZipEntry(f, dest, &budget); err != nil {
			return err
		}
	}
	return nil
}

func writeZipEntry(f *zip.File, dest string, budget *int64) error {
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

	// Cap total decompressed bytes to defend against zip bombs.
	n, err := io.Copy(out, io.LimitReader(rc, *budget+1))
	if err != nil {
		return err
	}
	if n > *budget {
		return &httpError{status: 400, msg: "zip archive is too large when decompressed"}
	}
	*budget -= n
	return nil
}
