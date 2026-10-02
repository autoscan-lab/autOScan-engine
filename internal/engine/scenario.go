package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ScenarioHostArg is the hidden argv mode that runs one multi-process scenario inside a single sandbox.
const ScenarioHostArg = "scenario-host"

// How long a process's output may stay open after it exits, held by children it left behind.
const scenarioWaitDelay = time.Second

type scenarioSpec struct {
	Processes []scenarioProcess `json:"processes"`
}

type scenarioProcess struct {
	Argv    []string `json:"argv"`
	Stdin   string   `json:"stdin,omitempty"`
	DelayMs int      `json:"delay_ms,omitempty"`
}

type scenarioOutcome struct {
	Index       int       `json:"index"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
	Stdout      string    `json:"stdout,omitempty"`
	Stderr      string    `json:"stderr,omitempty"`
	TimedOut    bool      `json:"timed_out,omitempty"`
	Killed      bool      `json:"killed,omitempty"`
	CrashReason string    `json:"crash_reason,omitempty"`
	StartFailed bool      `json:"start_failed,omitempty"`
}

// RunScenarioHost reads a scenario spec on stdin and writes one JSON line per process as it finishes.
func RunScenarioHost() int {
	log.SetPrefix("scenario-host: ")
	log.SetFlags(0)

	// Raise loopback so the processes can talk over 127.0.0.1, then drop CAP_NET_ADMIN before any of them start.
	if err := RaiseLoopback(); err != nil {
		log.Printf("loopback: %v", err)
	}
	DropAmbientCaps()

	var spec scenarioSpec
	if err := json.NewDecoder(os.Stdin).Decode(&spec); err != nil {
		log.Printf("reading spec: %v", err)
		return 1
	}
	dir, err := os.Getwd()
	if err != nil {
		log.Printf("getwd: %v", err)
		return 1
	}

	var mu sync.Mutex
	encoder := json.NewEncoder(os.Stdout)
	runScenario(context.Background(), dir, spec, func(outcome scenarioOutcome) {
		mu.Lock()
		defer mu.Unlock()
		_ = encoder.Encode(outcome)
	})
	return 0
}

func runScenario(ctx context.Context, dir string, spec scenarioSpec, emit func(scenarioOutcome)) {
	var wg sync.WaitGroup
	for index, proc := range spec.Processes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			emit(runScenarioProcess(ctx, dir, index, proc))
		}()
	}
	wg.Wait()
}

func runScenarioProcess(ctx context.Context, dir string, index int, proc scenarioProcess) scenarioOutcome {
	outcome := scenarioOutcome{Index: index}
	if proc.DelayMs > 0 {
		select {
		case <-time.After(time.Duration(proc.DelayMs) * time.Millisecond):
		case <-ctx.Done():
			outcome.Killed = true
			return outcome
		}
	}

	outcome.StartedAt = time.Now()
	ctx, cancel := context.WithTimeout(ctx, DefaultExecTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, proc.Argv[0], proc.Argv[1:]...)
	configureProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = scenarioWaitDelay
	cmd.Dir = dir
	cmd.Env = MinimalEnv(dir)
	if proc.Stdin != "" {
		cmd.Stdin = strings.NewReader(proc.Stdin)
	}
	stdout := &cappedBuffer{limit: maxCapturedOutput}
	stderr := &cappedBuffer{limit: maxCapturedOutput}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	outcome.FinishedAt = time.Now()

	outcome.Killed = ctx.Err() != nil
	outcome.TimedOut = ctx.Err() == context.DeadlineExceeded
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			outcome.StartFailed = true
			if stderr.Len() == 0 {
				stderr.WriteString(err.Error())
			}
		}
	}
	outcome.CrashReason = crashReasonFromExit(err, outcome.TimedOut)
	outcome.Stdout = stdout.String()
	outcome.Stderr = stderr.String()
	return outcome
}

// executeScenario runs every process of a scenario in one sandbox, so they share its network, PIDs, and IPC.
// It returns the outcomes it got, keyed by process index, and whether the scenario as a whole ran out of time.
func executeScenario(ctx context.Context, dir string, spec scenarioSpec, timeout time.Duration) (map[int]scenarioOutcome, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	outcomes := make(map[int]scenarioOutcome, len(spec.Processes))
	if !sandboxAvailable() {
		var mu sync.Mutex
		runScenario(ctx, dir, spec, func(outcome scenarioOutcome) {
			mu.Lock()
			defer mu.Unlock()
			outcomes[outcome.Index] = outcome
		})
		return outcomes, ctx.Err() == context.DeadlineExceeded, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return outcomes, false, fmt.Errorf("resolving executable: %w", err)
	}
	payload, err := json.Marshal(spec)
	if err != nil {
		return outcomes, false, err
	}

	argv, cleanup := sandboxCommand(sandboxSpec{
		workDir:  dir,
		readOnly: existingPaths(exe),
		netAdmin: true,
	}, []string{exe, ScenarioHostArg})
	defer cleanup()

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	configureProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = scenarioWaitDelay
	cmd.Dir = dir
	cmd.Env = MinimalEnv(dir)
	cmd.Stdin = bytes.NewReader(payload)
	hostLog := &cappedBuffer{limit: maxCapturedOutput}
	cmd.Stderr = hostLog
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return outcomes, false, err
	}
	if err := cmd.Start(); err != nil {
		return outcomes, false, err
	}

	decoder := json.NewDecoder(stdout)
	for {
		var outcome scenarioOutcome
		if decoder.Decode(&outcome) != nil {
			break
		}
		outcomes[outcome.Index] = outcome
	}
	waitErr := cmd.Wait()

	timedOut := ctx.Err() == context.DeadlineExceeded
	if waitErr != nil && ctx.Err() == nil && len(outcomes) < len(spec.Processes) {
		return outcomes, false, fmt.Errorf("scenario sandbox failed: %v %s", waitErr, strings.TrimSpace(hostLog.String()))
	}
	return outcomes, timedOut, nil
}
