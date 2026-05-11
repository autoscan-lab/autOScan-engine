<h1 align="center">autOScan Engine</h1>

<p align="center">
  <strong>Shared grading engine for C lab submission analysis</strong>
</p>

<p align="center">
  <a href="#"><img src="https://img.shields.io/badge/go-1.22+-00ADD8?style=flat&logo=go&logoColor=white" /></a>
  <a href="#"><img src="https://img.shields.io/badge/type-engine-1f6feb?style=flat" /></a>
  <a href="#"><img src="https://img.shields.io/badge/compiler-gcc-A42E2B?style=flat" /></a>
  <a href="#"><img src="https://img.shields.io/badge/runtime-valgrind-5c6bc0?style=flat" /></a>
</p>

<p align="center">
  Core engine and cloud grading service used by autOScan apps and tools
</p>

---

## Cloud Service

This repo also ships the private cloud grading service used by the Web
Agent. For service setup, deployment, environment variables, and HTTP API
details, see [CLOUD_SETUP.md](./CLOUD_SETUP.md).

## Features

- Submission discovery from root folders
- Parallel compilation pipeline with worker controls
- Single-process execution with args/stdin/test-case support
- Multi-process execution: spawns every configured executable concurrently and returns one buffered result per scenario
- Always-on Valgrind validation for leaks, reachable memory, memory errors, and open file descriptors
- Banned function scanning with file/line/column/snippet evidence
- Similarity analysis via C token fingerprinting
- AI-pattern detection against dictionary fingerprints
- Policy parsing and validation helpers
- Public Go facade package (`pkg/engine`) for autOScan apps/tools

---

## Installation

Requires: Go 1.22+, `gcc`, `valgrind`

```bash
go build ./...
```

## Usage

### As a Go Module

```bash
go get github.com/autoscan-lab/autoscan-engine@latest
```

```go
import (
  "context"

  engine "github.com/autoscan-lab/autoscan-engine/pkg/engine"
  "github.com/autoscan-lab/autoscan-engine/pkg/policy"
)

p, _ := policy.Load("/path/to/policy.yaml")
runner, _ := engine.NewRunner(p, engine.WithWorkers(4))
report, _ := runner.Run(context.Background(), "/path/to/submissions", engine.RunnerCallbacks{})
_ = report
```

### Multi-process execution

For policies with `run.multi_process.enabled: true`, use the executor directly:

```go
executor := engine.NewExecutorWithOptions(p, binaryDir, false)
result := executor.ExecuteMultiProcess(ctx, submission)
// or, with a scenario override:
result = executor.ExecuteMultiProcessScenario(ctx, submission, scenario)
```

Every executable in `run.multi_process.executables` is launched concurrently. The
call blocks until each process exits (or `ctx` is cancelled) and returns one
`*domain.MultiProcessResult` with buffered stdout/stderr per process.

## Package Layout

```text
autOScan-engine/
├── pkg/engine/            # Public facade API
├── pkg/domain/            # Shared engine models/results
├── pkg/policy/            # Policy models/loading/helpers
├── pkg/ai/                # AI dictionary parsing/validation
└── internal/engine/       # Engine internals
```

---

## Runtime Paths

Engine runtime behavior is compatible with:

```text
~/.config/autoscan/
├── libraries/
├── test_files/
├── expected_outputs/
└── banned.yaml
```

When embedding the engine, set `Policy.ConfigDir` or load with
`policy.LoadWithGlobalsFromConfigDir`.

For HTTP service setup, required environment variables, and Fly deployment,
see [CLOUD_SETUP.md](./CLOUD_SETUP.md).

---

## License

MIT
