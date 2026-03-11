# Architecture

## Purpose

Reusable grading core with no UI dependencies.

## Boundaries

- Public entrypoint: `pkg/engine`
- Public models/contracts: `pkg/domain`, `pkg/policy`, `pkg/ai`, `pkg/export`
- Internal implementation: `internal/engine`

## Core Capabilities

- Discovery: leaf submission folders + C source collection
- Compilation: policy-driven GCC compilation with concurrency controls
- Execution: single-process and multi-process runtime execution
- Scanning: banned function detection with source evidence
- Analysis: similarity and AI-pattern fingerprint comparison
- Reporting: aggregated run report + JSON/CSV exports

## Public API (`pkg/engine`)

- `NewRunner(policy, opts...)`
- `Runner.Run(ctx, root, callbacks)`
- `Runner.Cleanup()`
- `WithWorkers(n)`, `WithOutputDir(dir)`, `WithShortNames(enabled)`
- `NewExecutorWithOptions(policy, binaryDir, shortNames)`
- `ComputeSimilarityForProcess(submissions, srcFile, cfg)`
- `ComputeAIDetectionForProcess(submissions, srcFile, dict, cfg)`

## Runtime Paths (compatible)

- `~/.config/autoscan/libraries`
- `~/.config/autoscan/test_files`
- `~/.config/autoscan/expected_outputs`
- `~/.config/autoscan/banned.yaml`
