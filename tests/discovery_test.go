package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/autoscan-lab/autoscan-engine/internal/engine"
	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

func TestDiscoverSkipsMacOSMetadata(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"S1-submissions-C/Ana_1_assignsubmission_file/S1.c":            "int main(void) { return 0; }\n",
		"S1-submissions-C/Ana_1_assignsubmission_file/._S1.c":          "\x00\x05\x16\x07",
		"__MACOSX/S1-submissions-C/Ana_1_assignsubmission_file/._S1.c": "\x00\x05\x16\x07",
	}
	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	subs, err := engine.NewDiscoveryEngine(&policy.Policy{}).Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 {
		t.Fatalf("expected 1 submission, got %d: %+v", len(subs), subs)
	}
	if got := subs[0].CFiles; len(got) != 1 || got[0] != "S1.c" {
		t.Fatalf("expected only S1.c, got %v", got)
	}
}
