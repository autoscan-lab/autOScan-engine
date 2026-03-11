<h1 align="center">autOScan Engine</h1>

<p align="center">
  <strong>Shared grading engine for C lab submission analysis</strong>
</p>

<p align="center">
  <a href="#"><img src="https://img.shields.io/badge/go-1.22+-00ADD8?style=flat&logo=go&logoColor=white" /></a>
  <a href="#"><img src="https://img.shields.io/badge/type-engine-1f6feb?style=flat" /></a>
  <a href="#"><img src="https://img.shields.io/badge/compiler-gcc-A42E2B?style=flat" /></a>
</p>

<p align="center">
  Core module used by autOScan clients (TUI, GUI, and automation tooling)
</p>

---

## Features

- Submission discovery from root folders
- Parallel compilation pipeline with worker controls
- Single-process execution with args/stdin/test-case support
- Multi-process execution with live process output aggregation
- Banned function scanning with file/line/column/snippet evidence
- Similarity analysis via C token fingerprinting
- AI-pattern detection against dictionary fingerprints
- Policy parsing and validation helpers
- JSON and CSV export helpers for run reports
- Public Go facade package (`pkg/engine`) for consumer apps

---

## Installation

### Build from Source

```bash
git clone <your-private-repo-url>
cd autOScan-engine
go mod tidy
go build ./...
```

### Install Smoke CLI

```bash
go install ./cmd/autoscan-engine
```

---

## Usage

### As a Go Module

```bash
go get github.com/felitrejos/autoscan-engine@latest
```

```go
import (
  "context"

  engine "github.com/felitrejos/autoscan-engine/pkg/engine"
  "github.com/felitrejos/autoscan-engine/pkg/policy"
)

p, _ := policy.Load("/path/to/policy.yaml")
runner, _ := engine.NewRunner(p, engine.WithWorkers(4))
report, _ := runner.Run(context.Background(), "/path/to/submissions", engine.RunnerCallbacks{})
_ = report
```

### Smoke CLI

```bash
autoscan-engine run \
  -policy /path/to/policy.yaml \
  -root /path/to/submissions
```

```bash
autoscan-engine similarity \
  -policy /path/to/policy.yaml \
  -root /path/to/submissions \
  -source-file lab03.c
```

```bash
autoscan-engine ai-detect \
  -policy /path/to/policy.yaml \
  -root /path/to/submissions \
  -source-file lab03.c \
  -dictionary /path/to/ai_dictionary.yaml
```

---

## Package Layout

```text
autOScan-engine/
├── cmd/autoscan-engine/   # Optional smoke CLI
├── pkg/engine/            # Public facade API
├── pkg/domain/            # Shared engine models/results
├── pkg/policy/            # Policy models/loading/helpers
├── pkg/ai/                # AI dictionary parsing/validation
├── pkg/export/            # JSON/CSV report exporters
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

---

## Docs

- `docs/ARCHITECTURE.md`
- `docs/PRODUCT_SCOPE.md`
