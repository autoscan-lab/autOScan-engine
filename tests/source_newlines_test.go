package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/autoscan-lab/autoscan-engine/internal/engine"
	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

func TestScanReportsLinesForCROnlySource(t *testing.T) {
	dir := t.TempDir()
	source := "#include <stdio.h>\rint main(void) {\r  printf(\"hi\");\r  return 0;\r}\r"
	if err := os.WriteFile(filepath.Join(dir, "main.c"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	scanner := engine.NewScanEngine(&policy.Policy{BannedFunctions: []string{"printf"}})
	sub := domain.NewSubmission("s1", dir, []string{"main.c"})
	results := scanner.ScanAll([]domain.Submission{sub}, nil)

	if len(results) != 1 || len(results[0].Hits) != 1 {
		t.Fatalf("expected one banned hit, got %+v", results)
	}
	if got := results[0].Hits[0].Line; got != 3 {
		t.Fatalf("expected hit on line 3, got %d", got)
	}
}

func TestReadSourceFileNormalizesLineEndings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.c")
	if err := os.WriteFile(path, []byte("a\rb\r\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := domain.ReadSourceFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "a\nb\nc\n" {
		t.Fatalf("unexpected content %q", data)
	}
}
