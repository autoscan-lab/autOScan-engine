package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

// The multi-process shape the app writes.
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

func TestPolicyNamesInstancesOfOneSource(t *testing.T) {
	p := loadPolicyYaml(t, `name: S4_BC
run:
  multi_process:
    enabled: true
    executables:
      - source_file: S4_server.c
      - source_file: S4_client.c
        name: client_forward
      - source_file: S4_client.c
        name: client_inverted
`)
	var names, binaries []string
	for _, proc := range p.Run.MultiProcess.Executables {
		names = append(names, proc.Name())
		binaries = append(binaries, proc.Binary())
	}
	if got := strings.Join(names, ","); got != "S4_server,client_forward,client_inverted" {
		t.Fatalf("names = %s", got)
	}
	if got := strings.Join(binaries, ","); got != "S4_server,S4_client,S4_client" {
		t.Fatalf("binaries = %s", got)
	}
}

func TestPolicyRejectsDuplicateProcessNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yml")
	yaml := `name: S4_BC
run:
  multi_process:
    enabled: true
    executables:
      - source_file: S4_client.c
      - source_file: S4_client.c
`
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.Load(path); err == nil || !strings.Contains(err.Error(), "S4_client") {
		t.Fatalf("want an error naming the duplicate process, got %v", err)
	}
}

func loadPolicyYaml(t *testing.T, yaml string) *policy.Policy {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.yml")
	if err := os.WriteFile(path, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := policy.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
