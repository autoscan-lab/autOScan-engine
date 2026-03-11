# autOScan Engine

Core grading engine for the autOScan ecosystem.

This repository is intended to host shared grading logic used by multiple clients:
- `autOScan` TUI
- future `autOScan Studio` macOS GUI
- potential CI/batch tooling

## Status

Scaffolded repository with architecture and migration documentation.
Code extraction from the current `autOScan` repository is planned next.

## Goals

1. Keep grading behavior consistent across all clients.
2. Separate core logic from UI concerns.
3. Make engine versioning and testing independent from interface apps.

## Planned Scope

- Submission discovery
- Compilation pipeline
- Runtime execution (single and multi-process)
- Banned call scanning
- Similarity and AI-detection analysis
- Policy loading/validation
- Structured report output

## Planned Non-Goals (v1)

- UI rendering
- editor widgets
- terminal UX
- SSH connection management

## Initial Repository Layout (planned)

```text
autOScan-engine/
  cmd/                    # optional CLI wrappers around engine APIs
  pkg/engine/             # public engine facade and orchestrators
  internal/...            # implementation details
  docs/
    ARCHITECTURE.md
    MIGRATION_PLAN.md
    PRODUCT_SCOPE.md
```

## Next Step

Follow `docs/MIGRATION_PLAN.md` to extract engine packages from `autOScan` while preserving behavior.
