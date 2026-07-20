package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autoscan-lab/autoscan-engine/internal/engine"
	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

const libHeader = `char *readUntil(int fd, char end);
`

const libSource = `#include <unistd.h>
char *readUntil(int fd, char end) { (void)fd; (void)end; return 0; }
`

// A submission that never includes the library header: it implemented the
// function itself, so the library objects must not be linked.
const selfContainedSource = `#include <unistd.h>
char *readUntil(int fd, char end) { (void)fd; (void)end; return 0; }
int main(void) { return readUntil(0, '\n') == 0 ? 0 : 1; }
`

// A submission that includes the library header and relies on its object.
const libUserSource = `#include "lib.h"
int main(void) { return readUntil(0, '\n') == 0 ? 0 : 1; }
`

func buildLibrary(t *testing.T, configDir string) {
	t.Helper()

	libDir := filepath.Join(configDir, "libraries")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(libDir, "lib.h"), []byte(libHeader), 0644); err != nil {
		t.Fatal(err)
	}
	libC := filepath.Join(libDir, "lib.c")
	if err := os.WriteFile(libC, []byte(libSource), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("gcc", "-c", libC, "-o", filepath.Join(libDir, "lib.o"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building lib.o: %v\n%s", err, out)
	}
}

func writeSubmission(t *testing.T, dir string, files map[string]string) domain.Submission {
	t.Helper()

	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	var cFiles []string
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		cFiles = append(cFiles, name)
	}

	return domain.NewSubmission(filepath.Base(dir), dir, cFiles)
}

func multiProcessPolicy(configDir string) *policy.Policy {
	return &policy.Policy{
		Name: "test",
		Compile: policy.CompileConfig{
			GCC:   "gcc",
			Flags: []string{"-Wall"},
		},
		Run: policy.RunConfig{
			MultiProcess: &policy.MultiProcessConfig{
				Enabled: true,
				Executables: []policy.ProcessConfig{
					{SourceFile: "client.c"},
					{SourceFile: "server.c"},
				},
			},
		},
		LibraryFiles: []string{"lib.h", "lib.o"},
		ConfigDir:    configDir,
	}
}

func compileOne(t *testing.T, p *policy.Policy, sub domain.Submission) domain.CompileResult {
	t.Helper()

	e, err := engine.NewCompileEngine(p, engine.WithWorkers(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Cleanup() })

	results := e.CompileAll(context.Background(), []domain.Submission{sub}, nil)
	return results[0]
}

func TestMultiProcessCompileSkipsLibraryWhenNotIncluded(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	configDir := t.TempDir()
	buildLibrary(t, configDir)

	sub := writeSubmission(t, filepath.Join(t.TempDir(), "student"), map[string]string{
		"client.c": selfContainedSource,
		"server.c": selfContainedSource,
	})

	result := compileOne(t, multiProcessPolicy(configDir), sub)
	if !result.OK {
		t.Fatalf("expected self-contained submission to compile without the library, stderr:\n%s", result.Stderr)
	}
}

func TestMultiProcessCompileLinksLibraryWhenIncluded(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	configDir := t.TempDir()
	buildLibrary(t, configDir)

	sub := writeSubmission(t, filepath.Join(t.TempDir(), "student"), map[string]string{
		"client.c": libUserSource,
		"server.c": libUserSource,
	})

	result := compileOne(t, multiProcessPolicy(configDir), sub)
	if !result.OK {
		t.Fatalf("expected compile against library to succeed, stderr:\n%s", result.Stderr)
	}
}

func TestMultiProcessCompileRealErrorsStillFail(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	configDir := t.TempDir()
	buildLibrary(t, configDir)

	sub := writeSubmission(t, filepath.Join(t.TempDir(), "student"), map[string]string{
		"client.c": "int main(void) { return oops; }\n",
		"server.c": libUserSource,
	})

	result := compileOne(t, multiProcessPolicy(configDir), sub)
	if result.OK {
		t.Fatal("expected compile to fail")
	}
	if !strings.Contains(result.Stderr, "=== client ===") {
		t.Fatalf("expected client section in stderr, got:\n%s", result.Stderr)
	}
}

func singleProcessPolicy(configDir string) *policy.Policy {
	return &policy.Policy{
		Name: "test",
		Compile: policy.CompileConfig{
			GCC:        "gcc",
			Flags:      []string{"-Wall"},
			SourceFile: "main.c",
		},
		LibraryFiles: []string{"lib.h", "lib.o"},
		ConfigDir:    configDir,
	}
}

func TestSingleProcessCompileLinksLibraryWhenIncluded(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	configDir := t.TempDir()
	buildLibrary(t, configDir)

	sub := writeSubmission(t, filepath.Join(t.TempDir(), "student"), map[string]string{
		"main.c": libUserSource,
	})

	result := compileOne(t, singleProcessPolicy(configDir), sub)
	if !result.OK {
		t.Fatalf("expected compile against library to succeed, stderr:\n%s", result.Stderr)
	}
}

func TestSingleProcessCompileSkipsLibraryWhenNotIncluded(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	configDir := t.TempDir()
	buildLibrary(t, configDir)

	sub := writeSubmission(t, filepath.Join(t.TempDir(), "student"), map[string]string{
		"main.c": selfContainedSource,
	})

	result := compileOne(t, singleProcessPolicy(configDir), sub)
	if !result.OK {
		t.Fatalf("expected self-contained submission to compile without the library, stderr:\n%s", result.Stderr)
	}
}

// A commented-out include must not count as using the library; if the engine
// linked it anyway, the duplicate readUntil would fail the build.
func TestCommentedIncludeDoesNotLinkLibrary(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	configDir := t.TempDir()
	buildLibrary(t, configDir)

	sub := writeSubmission(t, filepath.Join(t.TempDir(), "student"), map[string]string{
		"main.c": "// #include \"lib.h\"\n" + selfContainedSource,
	})

	result := compileOne(t, singleProcessPolicy(configDir), sub)
	if !result.OK {
		t.Fatalf("expected commented include to be ignored, stderr:\n%s", result.Stderr)
	}
}

// Angle-bracket includes resolve through -I to the library dir and must link.
func TestAngleBracketIncludeLinksLibrary(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	configDir := t.TempDir()
	buildLibrary(t, configDir)

	sub := writeSubmission(t, filepath.Join(t.TempDir(), "student"), map[string]string{
		"main.c": "#include <lib.h>\nint main(void) { return readUntil(0, '\\n') == 0 ? 0 : 1; }\n",
	})

	result := compileOne(t, singleProcessPolicy(configDir), sub)
	if !result.OK {
		t.Fatalf("expected angle-bracket include to link the library, stderr:\n%s", result.Stderr)
	}
}

// With no headers among the library files there is nothing to detect, so the
// objects are always linked; a submission relying on them must still build.
func TestLibraryWithoutHeadersAlwaysLinks(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	configDir := t.TempDir()
	buildLibrary(t, configDir)

	p := singleProcessPolicy(configDir)
	p.LibraryFiles = []string{"lib.o"}

	sub := writeSubmission(t, filepath.Join(t.TempDir(), "student"), map[string]string{
		"main.c": "char *readUntil(int fd, char end);\nint main(void) { return readUntil(0, '\\n') == 0 ? 0 : 1; }\n",
	})

	result := compileOne(t, p, sub)
	if !result.OK {
		t.Fatalf("expected header-less library to always link, stderr:\n%s", result.Stderr)
	}
}
