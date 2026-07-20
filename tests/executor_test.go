package tests

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/autoscan-lab/autoscan-engine/internal/engine"
	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

// Prints its args and echoes the first line of stdin, so a test can assert
// exactly what the executor passed in.
const printerSource = `#include <stdio.h>
int main(int argc, char **argv) {
	for (int i = 1; i < argc; i++) printf("arg:%s\n", argv[i]);
	char buf[64];
	if (fgets(buf, sizeof buf, stdin)) printf("in:%s", buf);
	return 0;
}
`

const trivialSource = `int main(void) { return 0; }
`

func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not available", name)
	}
}

func executorPolicy(configDir string, sourceFiles ...string) *policy.Policy {
	executables := make([]policy.ProcessConfig, len(sourceFiles))
	for i, sourceFile := range sourceFiles {
		executables[i] = policy.ProcessConfig{SourceFile: sourceFile}
	}

	return &policy.Policy{
		Name: "test",
		Compile: policy.CompileConfig{
			GCC:   "gcc",
			Flags: []string{"-Wall"},
		},
		Run: policy.RunConfig{
			MultiProcess: &policy.MultiProcessConfig{
				Enabled:     true,
				Executables: executables,
			},
		},
		ConfigDir: configDir,
	}
}

// compileAndExecutor compiles the submission into a shared binary dir and
// returns an executor rooted there, mirroring how grade.go wires the two.
func compileAndExecutor(t *testing.T, p *policy.Policy, sub domain.Submission) *engine.Executor {
	t.Helper()

	binDir := t.TempDir()
	e, err := engine.NewCompileEngine(p, engine.WithWorkers(1), engine.WithOutputDir(binDir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Cleanup() })

	results := e.CompileAll(context.Background(), []domain.Submission{sub}, nil)
	if !results[0].OK {
		t.Fatalf("compile failed: %s", results[0].Stderr)
	}

	return engine.NewExecutorWithOptions(p, binDir, false)
}

func TestScenarioArgsAndStdinReachProcess(t *testing.T) {
	requireTool(t, "gcc")
	requireTool(t, "valgrind") // every execution runs under valgrind

	sub := writeSubmission(t, filepath.Join(t.TempDir(), "student"), map[string]string{
		"printer.c": printerSource,
	})
	executor := compileAndExecutor(t, executorPolicy(t.TempDir(), "printer.c"), sub)

	scenario := policy.MultiProcessScenario{
		Name:          "Basic",
		ProcessArgs:   map[string][]string{"printer": {"hello", "world"}},
		ProcessInputs: map[string]string{"printer": "ping\n"},
	}
	result := executor.ExecuteMultiProcessScenario(context.Background(), sub, scenario)
	if result == nil {
		t.Fatal("expected a result")
	}

	got := result.Processes["printer"].Stdout
	want := "arg:hello\narg:world\nin:ping\n"
	if got != want {
		t.Fatalf("stdout mismatch\nwant: %q\ngot:  %q", want, got)
	}
}

func TestNoScenarioRunsBare(t *testing.T) {
	requireTool(t, "gcc")
	requireTool(t, "valgrind")

	sub := writeSubmission(t, filepath.Join(t.TempDir(), "student"), map[string]string{
		"printer.c": printerSource,
	})
	executor := compileAndExecutor(t, executorPolicy(t.TempDir(), "printer.c"), sub)

	result := executor.ExecuteMultiProcess(context.Background(), sub)
	if result == nil {
		t.Fatal("expected a result")
	}

	if got := result.Processes["printer"].Stdout; got != "" {
		t.Fatalf("expected no args or stdin without a scenario, got stdout: %q", got)
	}
}

func TestScenarioDelayStaggersProcessStart(t *testing.T) {
	requireTool(t, "gcc")
	// No valgrind requirement: StartedAt is stamped after the delay, before
	// the execution preflight, so the ordering holds even when running the
	// binary itself fails.

	sub := writeSubmission(t, filepath.Join(t.TempDir(), "student"), map[string]string{
		"first.c":  trivialSource,
		"second.c": trivialSource,
	})
	executor := compileAndExecutor(t, executorPolicy(t.TempDir(), "first.c", "second.c"), sub)

	scenario := policy.MultiProcessScenario{
		Name:          "Staggered",
		ProcessDelays: map[string]int{"second": 300},
	}
	result := executor.ExecuteMultiProcessScenario(context.Background(), sub, scenario)
	if result == nil {
		t.Fatal("expected a result")
	}

	gap := result.Processes["second"].StartedAt.Sub(result.Processes["first"].StartedAt)
	if gap < 200*time.Millisecond {
		t.Fatalf("expected second to start ~300ms after first, gap was %v", gap)
	}
}
