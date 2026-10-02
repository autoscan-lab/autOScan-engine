package tests

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autoscan-lab/autoscan-engine/internal/engine"
	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
	"github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

// Prints its args and echoes the first line of stdin.
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

	return engine.NewExecutor(p, binDir)
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
	requireTool(t, "valgrind")

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

func TestInstancesOfOneSourceRunWithTheirOwnArgs(t *testing.T) {
	requireTool(t, "gcc")
	requireTool(t, "valgrind")

	sub := writeSubmission(t, filepath.Join(t.TempDir(), "student"), map[string]string{
		"printer.c": printerSource,
	})
	p := executorPolicy(t.TempDir())
	p.Run.MultiProcess.Executables = []policy.ProcessConfig{
		{SourceFile: "printer.c", InstanceName: "forward"},
		{SourceFile: "printer.c", InstanceName: "inverted"},
	}
	executor := compileAndExecutor(t, p, sub)

	scenario := policy.MultiProcessScenario{
		Name: "Both",
		ProcessArgs: map[string][]string{
			"forward":  {"team_forward.txt"},
			"inverted": {"team_inverted.txt"},
		},
	}
	result := executor.ExecuteMultiProcessScenario(context.Background(), sub, scenario)
	if result == nil {
		t.Fatal("expected a result")
	}

	if got := strings.Join(result.Order, ","); got != "forward,inverted" {
		t.Fatalf("order = %q, want forward,inverted", got)
	}
	for name, want := range map[string]string{
		"forward":  "arg:team_forward.txt\n",
		"inverted": "arg:team_inverted.txt\n",
	} {
		proc := result.Processes[name]
		if proc == nil || proc.Stdout != want || proc.SourceFile != "printer.c" {
			t.Errorf("%s = %+v, want stdout %q from printer.c", name, proc, want)
		}
	}
}

// Accepts one connection on 127.0.0.1:argv[1] and prints what it reads.
const tcpServerSource = `#include <arpa/inet.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/socket.h>
#include <unistd.h>
int main(int argc, char **argv) {
	int fd = socket(AF_INET, SOCK_STREAM, 0), one = 1;
	setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &one, sizeof one);
	struct sockaddr_in addr = {0};
	addr.sin_family = AF_INET;
	addr.sin_port = htons(atoi(argv[1]));
	addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
	if (bind(fd, (struct sockaddr *)&addr, sizeof addr) < 0 || listen(fd, 1) < 0) { perror("listen"); return 1; }
	int c = accept(fd, NULL, NULL);
	char buf[64] = {0};
	ssize_t n = read(c, buf, sizeof buf - 1);
	printf("got:%.*s\n", (int)(n > 0 ? n : 0), buf);
	close(c);
	close(fd);
	return 0;
}
`

// Connects to 127.0.0.1:argv[1] and sends argv[2].
const tcpClientSource = `#include <arpa/inet.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>
int main(int argc, char **argv) {
	int fd = socket(AF_INET, SOCK_STREAM, 0);
	struct sockaddr_in addr = {0};
	addr.sin_family = AF_INET;
	addr.sin_port = htons(atoi(argv[1]));
	addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
	if (connect(fd, (struct sockaddr *)&addr, sizeof addr) < 0) { perror("connect"); return 1; }
	write(fd, argv[2], strlen(argv[2]));
	close(fd);
	return 0;
}
`

func TestScenarioProcessesShareLoopback(t *testing.T) {
	requireTool(t, "gcc")
	requireTool(t, "valgrind")

	sub := writeSubmission(t, filepath.Join(t.TempDir(), "student"), map[string]string{
		"server.c": tcpServerSource,
		"client.c": tcpClientSource,
	})
	executor := compileAndExecutor(t, executorPolicy(t.TempDir(), "server.c", "client.c"), sub)

	scenario := policy.MultiProcessScenario{
		Name:          "Connect",
		ProcessArgs:   map[string][]string{"server": {"5701"}, "client": {"5701", "hello"}},
		ProcessDelays: map[string]int{"client": 1000},
	}
	result := executor.ExecuteMultiProcessScenario(context.Background(), sub, scenario)
	if result == nil {
		t.Fatal("expected a result")
	}

	if got := result.Processes["server"].Stdout; got != "got:hello\n" {
		t.Fatalf("server stdout = %q, stderr = %q; client stderr = %q",
			got, result.Processes["server"].Stderr, result.Processes["client"].Stderr)
	}
	if !result.Processes["client"].Passed {
		t.Fatalf("client failed: %+v", result.Processes["client"])
	}
}

// Prints the first line of the file named by argv[1], then overwrites it.
const fileProbeSource = `#include <stdio.h>
int main(int argc, char **argv) {
	if (argc < 2) return 1;
	FILE *f = fopen(argv[1], "r+");
	if (!f) { printf("open failed\n"); return 0; }
	char buf[64] = {0};
	if (fgets(buf, sizeof buf, f)) printf("file:%s", buf);
	rewind(f);
	fputs("changed\n", f);
	fclose(f);
	return 0;
}
`

// Creates the file named by argv[1]; does nothing without args.
const fileWriterSource = `#include <stdio.h>
int main(int argc, char **argv) {
	if (argc < 2) return 0;
	FILE *f = fopen(argv[1], "w");
	if (!f) return 1;
	fputs("made\n", f);
	fclose(f);
	return 0;
}
`

func runPolicy(configDir, sourceFile string, testFiles ...string) *policy.Policy {
	return &policy.Policy{
		Name:      "test",
		Compile:   policy.CompileConfig{GCC: "gcc", Flags: []string{"-Wall"}, SourceFile: sourceFile},
		TestFiles: testFiles,
		ConfigDir: configDir,
	}
}

func writeConfigFile(t *testing.T, configDir, subdir, name, content string) {
	t.Helper()
	dir := filepath.Join(configDir, subdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTestFilesAreWritableAndFreshPerRun(t *testing.T) {
	requireTool(t, "gcc")
	requireTool(t, "valgrind")

	configDir := t.TempDir()
	writeConfigFile(t, configDir, "test_files", "data.txt", "original\n")
	sub := writeSubmission(t, filepath.Join(t.TempDir(), "student"), map[string]string{
		"probe.c": fileProbeSource,
	})
	executor := compileAndExecutor(t, runPolicy(configDir, "probe.c", "data.txt"), sub)

	// The second run must not see the first run's write.
	for run := 1; run <= 2; run++ {
		result := executor.Execute(context.Background(), sub, []string{"data.txt"}, "")
		if !result.OK || result.Stdout != "file:original\n" {
			t.Fatalf("run %d: ok=%v stdout=%q stderr=%q", run, result.OK, result.Stdout, result.Stderr)
		}
	}

	data, err := os.ReadFile(filepath.Join(configDir, "test_files", "data.txt"))
	if err != nil || string(data) != "original\n" {
		t.Fatalf("config copy was modified: %q (%v)", data, err)
	}
}

func TestMissingTestFileFailsTheRun(t *testing.T) {
	requireTool(t, "gcc")
	// No valgrind needed: staging fails before the execution preflight.

	sub := writeSubmission(t, filepath.Join(t.TempDir(), "student"), map[string]string{
		"probe.c": fileProbeSource,
	})
	executor := compileAndExecutor(t, runPolicy(t.TempDir(), "probe.c", "missing.txt"), sub)

	result := executor.Execute(context.Background(), sub, nil, "")
	if result.OK || !strings.Contains(result.Stderr, "missing.txt") {
		t.Fatalf("expected a staging failure naming missing.txt, got ok=%v stderr=%q", result.OK, result.Stderr)
	}
}

func TestStaleProducedFileDoesNotPass(t *testing.T) {
	requireTool(t, "gcc")
	requireTool(t, "valgrind")

	configDir := t.TempDir()
	writeConfigFile(t, configDir, "expected_outputs", "made.txt", "made\n")
	sub := writeSubmission(t, filepath.Join(t.TempDir(), "student"), map[string]string{
		"writer.c": fileWriterSource,
	})
	executor := compileAndExecutor(t, runPolicy(configDir, "writer.c"), sub)

	writes := policy.TestCase{Name: "writes", Args: []string{"out.txt"}, ProducedFile: "out.txt", ExpectedOutputFile: "made.txt"}
	if got := executor.ExecuteTestCase(context.Background(), sub, writes).OutputMatch; got != domain.OutputMatchPass {
		t.Fatalf("expected pass when the program writes the file, got %q", got)
	}

	skips := policy.TestCase{Name: "skips", ProducedFile: "out.txt", ExpectedOutputFile: "made.txt"}
	if got := executor.ExecuteTestCase(context.Background(), sub, skips).OutputMatch; got != domain.OutputMatchMissing {
		t.Fatalf("expected missing when the program does not write the file, got %q", got)
	}
}

func elfHeader(t *testing.T, fileType elf.Type) []byte {
	t.Helper()
	hdr := elf.Header64{Type: uint16(fileType), Machine: uint16(elf.EM_X86_64), Version: uint32(elf.EV_CURRENT), Ehsize: 64}
	copy(hdr.Ident[:], elf.ELFMAG)
	hdr.Ident[elf.EI_CLASS] = byte(elf.ELFCLASS64)
	hdr.Ident[elf.EI_DATA] = byte(elf.ELFDATA2LSB)
	hdr.Ident[elf.EI_VERSION] = byte(elf.EV_CURRENT)
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, hdr); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCopyFileMakesOnlyProgramsExecutable(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	files := map[string]struct {
		data       []byte
		executable bool
	}{
		"worm":     {elfHeader(t, elf.ET_DYN), true},
		"static":   {elfHeader(t, elf.ET_EXEC), true},
		"lib.o":    {elfHeader(t, elf.ET_REL), false},
		"data.bin": {[]byte{0x7f, 0x00, 0x01, 0x02}, false},
		"notes":    {[]byte("#!/bin/sh\necho hi\n"), false},
	}

	for name, file := range files {
		if err := os.WriteFile(filepath.Join(src, name), file.data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := engine.CopyFile(filepath.Join(src, name), filepath.Join(dst, name)); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(dst, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode()&0o111 != 0; got != file.executable {
			t.Errorf("%s: executable=%v, want %v", name, got, file.executable)
		}
	}
}
