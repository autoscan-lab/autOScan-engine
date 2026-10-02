# AGENTS.md

Guidance for coding agents working in this repo. `CLAUDE.md` is a symlink to
this file, so edit this one.

## Overview

autOScan-engine is the Go module `github.com/autoscan-lab/autoscan-engine`. It
contains the grading engine and the HTTP grading service in
`cmd/autoscan-server`. The service runs on Fly.io as one machine with a `/data`
volume (see `fly.toml`).

- **What it owns:** compilation, test execution under Valgrind,
  banned-function scanning, similarity, AI detection, multi-process runs,
  sandboxed interactive terminals (bubblewrap), and the run result payload.
- **What it reads and writes:** policies from R2 (`assignments/<name>/`,
  `banned.yaml`, `ai_dictionary.yaml`), and results back to R2. It knows
  nothing about users, workspaces, or grades, and never touches MongoDB.
- **Docs:** [README.md](README.md) covers the package, and
  [CLOUD_SETUP.md](CLOUD_SETUP.md) covers the service's environment, R2 layout,
  and endpoints.

## Layout

```text
pkg/engine/          public facade over internal/engine
pkg/domain/          result and report models
pkg/policy/          policy.yml models and loading
pkg/ai/              AI dictionary parsing
internal/engine/     compile, execute, sandbox, similarity, AI detection
internal/terminal/   terminal sessions, pane host, HMAC tokens
cmd/autoscan-server/ HTTP handlers, R2 sync, grade queue, run storage, idle exit
tests/               all Go tests, black-box over the exported surfaces
tests/e2e/           Docker stack with a fake R2 and end-to-end server tests
```

## Commands

```bash
go build ./...
go vet ./...
go test ./...    # what CI runs; skips tests/e2e
```

End-to-end tests against the real image, with SeaweedFS standing in for R2:

```bash
docker compose -f tests/e2e/compose.yml up -d --build
go test -tags e2e ./tests/e2e/ -count=1
docker compose -f tests/e2e/compose.yml down
```

- Sandboxing and terminals need Linux. On macOS, use the Docker stack.
- Execution tests skip without Valgrind. To run them sandboxed from macOS:
  `docker run --rm --privileged -v "$PWD":/src -w /src golang:1.25-bookworm
  sh -c 'apt-get update -qq && apt-get install -y -qq valgrind bubblewrap && go test ./tests/'`
- Each multi-process scenario runs in one sandbox: the engine re-execs itself
  as `scenario-host` (like `pane-host` for terminals), so its processes share
  loopback, PIDs, and IPC.
- A `GOOS=linux` cross-compile fails on cgo, so check Linux builds with the
  Docker build.
- `tests/e2e/README.md` lists the scenarios and how to add one.

## HTTP contract

- JSON is snake_case, and this repo defines the canonical shape. When an
  endpoint or field changes, update the endpoint list in `CLOUD_SETUP.md`.
- Terminal tokens are HMAC-signed with `ENGINE_SECRET` by the caller. Their
  claims are defined in `internal/terminal/token.go`.
- Contract changes should accept what callers already send, so the engine can
  deploy (`fly deploy`) before its callers update.

## Conventions

- Tests live only in `tests/`, never next to the code.
- Keep comments to one short line explaining why. No history or narration.
- Keep changes tight. Avoid drive-by refactors.
- **Limits:**
  - Grade jobs run one at a time; within a job, tests run one submission per
    CPU (multi-process labs stay sequential).
  - Terminals are capped at 16 sessions of up to 4 panes.
  - If you change either, keep the concurrency limits in `fly.toml` in line.
- Temporary files are fine while working. Delete them before finishing.
- Never commit secrets. `.env` is ignored, and `.env.example` lists the
  required variables.
- Work lands on `dev` and reaches `main` through a pull request. Deploy
  (`fly deploy`) from `main`.
