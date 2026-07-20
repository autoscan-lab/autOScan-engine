package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

// The multi-process contract the app writes: executables are bare source
// files (process name = stem), and args/stdin/delays live on scenarios.
const scenarioPolicyYaml = `name: S4_BC
compile:
  gcc: gcc
  flags: ["-Wall"]
run:
  test_cases: []
  multi_process:
    enabled: true
    executables:
      - source_file: S4_client.c
      - source_file: S4_server.c
    test_scenarios:
      - name: Basic
        process_args:
          S4_client: ["127.0.0.1", "8000"]
        process_inputs:
          S4_client: "h\n"
        process_delays:
          S4_client: 50
`

func TestPolicyParsesScenarioOwnedProcessConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yml")
	if err := os.WriteFile(path, []byte(scenarioPolicyYaml), 0644); err != nil {
		t.Fatal(err)
	}

	p, err := policy.LoadWithGlobalsFromConfigDir(path, dir)
	if err != nil {
		t.Fatal(err)
	}

	mp := p.Run.MultiProcess
	if mp == nil || !mp.Enabled {
		t.Fatal("expected an enabled multi_process block")
	}

	if len(mp.Executables) != 2 {
		t.Fatalf("expected 2 executables, got %d", len(mp.Executables))
	}
	if got := mp.Executables[0].Name(); got != "S4_client" {
		t.Fatalf("expected process name derived from source stem, got %q", got)
	}
	if got := mp.Executables[1].Name(); got != "S4_server" {
		t.Fatalf("expected process name derived from source stem, got %q", got)
	}

	if len(mp.TestScenarios) != 1 {
		t.Fatalf("expected 1 scenario, got %d", len(mp.TestScenarios))
	}
	scenario := mp.TestScenarios[0]
	if got := scenario.ProcessArgs["S4_client"]; len(got) != 2 || got[0] != "127.0.0.1" || got[1] != "8000" {
		t.Fatalf("unexpected scenario args: %v", got)
	}
	if got := scenario.ProcessInputs["S4_client"]; got != "h\n" {
		t.Fatalf("unexpected scenario input: %q", got)
	}
	if got := scenario.ProcessDelays["S4_client"]; got != 50 {
		t.Fatalf("unexpected scenario delay: %d", got)
	}
}
