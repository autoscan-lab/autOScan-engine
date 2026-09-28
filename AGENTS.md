# AGENTS.md

Guidance for coding agents working in this repo. `CLAUDE.md` is a symlink to
this file, so edit this one.

## Overview

autOScan-engine is the Go module `github.com/autoscan-lab/autoscan-engine`. It
contains the grading engine and the HTTP service in `cmd/autoscan-server`, which
the autOScan web app calls. The service runs on Fly.io as one machine with a
`/data` volume (see `fly.toml`).

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
- A `GOOS=linux` cross-compile fails on cgo, so check Linux builds with the
  Docker build.
- `tests/e2e/README.md` lists the scenarios and how to add one.

## Contract with the web app

- The autOScan web app is usually checked out next to this repo as
  `../autoscan-app`.
- JSON is snake_case, and this repo defines the canonical shape. When a field
  changes, update:
  - the app's `src/lib/engine/client.ts` and `src/lib/grading/results.ts`;
  - the endpoint list in `CLOUD_SETUP.md`;
  - the engine contract in the app's `ARCHITECTURE.md`.
- Terminal tokens are signed by the app with `ENGINE_SECRET`. Their claims in
  `internal/terminal/token.go` must match what the app's
  `src/app/api/terminal/route.ts` mints.
- Deploy first the side that tolerates the other's old version, which is
  usually the engine (`fly deploy`).

## Conventions

- Tests live only in `tests/`, never next to the code.
- Keep comments to one short line explaining why. No history or narration.
- Keep changes tight. Avoid drive-by refactors.
- **Limits:**
  - Grade jobs run one at a time.
  - Terminals are capped at 16 sessions of up to 4 panes.
  - If you change either, keep the concurrency limits in `fly.toml` in line.
- Temporary files are fine while working. Delete them before finishing.
- Never commit secrets. `.env` is ignored, and `.env.example` lists the
  required variables.
- Changes land on `main` through a pull request.
