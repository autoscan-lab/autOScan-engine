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
- Multi-process execution with live process output aggregation
- Always-on Valgrind validation for leaks, reachable memory, memory errors, and open file descriptors
- Banned function scanning with file/line/column/snippet evidence
- Similarity analysis via C token fingerprinting
- AI-pattern detection against dictionary fingerprints
- Policy parsing and validation helpers
- Public Go facade package (`pkg/engine`) for autOScan apps/tools
- `autoscan-bridge` companion CLI for GUI integrations

---

## Installation

Requires: Go 1.22+, `gcc`, `valgrind`

```bash
go build ./...
```

### Build the Studio Bridge

```bash
go build -o ./autoscan-bridge ./cmd/autoscan-bridge
./autoscan-bridge version
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

### As a Companion CLI

The bridge is intended for apps and services that need a stable process
boundary into the engine, including `autOScan Studio`, the cloud grading
service, and other third-party integrations.

Check capability flags:

```bash
./autoscan-bridge capabilities
```

Run a full workspace session (compile + scan):

```bash
./autoscan-bridge run-session \
  --workspace /path/to/submissions \
  --policy /path/to/policy.yaml
```

Run one policy test case for one submission:

```bash
./autoscan-bridge run-test-case \
  --workspace /path/to/submissions \
  --policy /path/to/policy.yaml \
  --submission-id "submission-id-from-discovery" \
  --test-case-index 0
```

The bridge writes newline-delimited JSON events to stdout:

- `started`
- `discovery_complete`
- `compile_complete`
- `scan_complete`
- `test_case_started`
- `test_case_complete`
- `tests_complete`
- `run_complete`
- `error`

`capabilities` returns a machine-readable JSON object with:

- `run_session`
- `run_test_case`
- `diff_payload`

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
`policy.LoadWithGlobalsFromConfigDir`. The bridge exposes the same override
through `--config-dir`.

For HTTP service setup, required environment variables, and Fly deployment,
see [CLOUD_SETUP.md](./CLOUD_SETUP.md).

---

## License

MIT
