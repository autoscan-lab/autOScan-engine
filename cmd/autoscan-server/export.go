package main

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

// buildAndUploadExport builds a zip of binaryDir (one folder per submission,
// each containing the compiled binary and a copy of every policy test file —
// valgrind logs are skipped) and uploads it to the given R2 key. The local zip
// file is removed after upload.
func buildAndUploadExport(ctx context.Context, cfg config, binaryDir string, p *policy.Policy, exportKey string) error {
	if err := copyTestFilesIntoSubmissions(binaryDir, p); err != nil {
		return fmt.Errorf("copying test files: %w", err)
	}

	zipPath, err := zipBinaryDir(binaryDir)
	if err != nil {
		return fmt.Errorf("building export zip: %w", err)
	}
	defer os.Remove(zipPath)

	r2, err := newR2Client(ctx, cfg)
	if err != nil {
		return fmt.Errorf("creating r2 client: %w", err)
	}
	if err := r2.uploadObject(ctx, exportKey, zipPath, "application/zip"); err != nil {
		return err
	}
	return nil
}

func copyTestFilesIntoSubmissions(binaryDir string, p *policy.Policy) error {
	if len(p.TestFiles) == 0 {
		return nil
	}

	testFilesDir := filepath.Join(p.EffectiveConfigDir(), "test_files")

	entries, err := os.ReadDir(binaryDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		submissionDir := filepath.Join(binaryDir, entry.Name())
		for _, name := range p.TestFiles {
			src := filepath.Join(testFilesDir, name)
			info, statErr := os.Stat(src)
			if statErr != nil || info.IsDir() {
				continue
			}
			dst := filepath.Join(submissionDir, name)
			if err := copyFile(src, dst); err != nil {
				return fmt.Errorf("copying %s into %s: %w", name, entry.Name(), err)
			}
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

func zipBinaryDir(binaryDir string) (string, error) {
	zipFile, err := os.CreateTemp("", "autoscan-export-*.zip")
	if err != nil {
		return "", err
	}
	zipPath := zipFile.Name()

	writer := zip.NewWriter(zipFile)

	walkErr := filepath.Walk(binaryDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == binaryDir {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if shouldSkipExportFile(info.Name()) {
			return nil
		}

		rel, err := filepath.Rel(binaryDir, path)
		if err != nil {
			return err
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		header.Method = zip.Deflate

		entry, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		if _, err := io.Copy(entry, file); err != nil {
			return err
		}
		return nil
	})

	closeErr := writer.Close()
	fileCloseErr := zipFile.Close()

	if walkErr != nil {
		os.Remove(zipPath)
		return "", walkErr
	}
	if closeErr != nil {
		os.Remove(zipPath)
		return "", closeErr
	}
	if fileCloseErr != nil {
		os.Remove(zipPath)
		return "", fileCloseErr
	}
	return zipPath, nil
}

func shouldSkipExportFile(name string) bool {
	return strings.HasPrefix(name, "valgrind-") && strings.HasSuffix(name, ".log")
}
