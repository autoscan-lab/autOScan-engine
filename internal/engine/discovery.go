package engine

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

// macOSMetadataDir holds AppleDouble copies of every file in archives made by macOS "Compress".
const macOSMetadataDir = "__MACOSX"

type DiscoveryEngine struct {
	policy *policy.Policy
}

func NewDiscoveryEngine(p *policy.Policy) *DiscoveryEngine {
	return &DiscoveryEngine{policy: p}
}

// Discover finds all leaf folders that contain at least one .c file.
func (e *DiscoveryEngine) Discover(root string) ([]domain.Submission, error) {
	var submissions []domain.Submission

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	err = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() {
			return nil
		}

		if (strings.HasPrefix(d.Name(), ".") || d.Name() == macOSMetadataDir) && path != absRoot {
			return filepath.SkipDir
		}

		isLeaf, cFiles, err := e.checkLeafFolder(path)
		if err != nil {
			return err
		}

		if isLeaf && len(cFiles) > 0 {
			relPath, err := filepath.Rel(absRoot, path)
			if err != nil {
				relPath = d.Name()
			}

			submissions = append(submissions, domain.NewSubmission(
				relPath,
				path,
				cFiles,
			))
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return submissions, nil
}

func (e *DiscoveryEngine) checkLeafFolder(dir string) (bool, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, nil, err
	}

	var cFiles []string
	hasSubdirs := false

	for _, entry := range entries {
		if entry.IsDir() {
			if !strings.HasPrefix(entry.Name(), ".") && entry.Name() != macOSMetadataDir {
				hasSubdirs = true
			}
			continue
		}

		// AppleDouble "._name.c" files are macOS metadata, not source.
		if strings.HasPrefix(entry.Name(), "._") {
			continue
		}

		if strings.HasSuffix(strings.ToLower(entry.Name()), ".c") {
			cFiles = append(cFiles, entry.Name())
		}
	}

	return !hasSubdirs, cFiles, nil
}
