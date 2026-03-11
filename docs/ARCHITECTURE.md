# Architecture

## Purpose

Provide a single, testable grading core consumed by multiple UIs/tools.

## Design Principles

1. UI-agnostic engine APIs
2. deterministic outputs for the same inputs
3. explicit data contracts between engine and clients
4. clear package boundaries for maintainability

## Components

1. Discovery
- traverses submission roots
- identifies leaf submission folders and C sources

2. Compilation
- builds gcc commands from policy
- compiles in parallel with worker limits
- returns structured compile results

3. Execution
- executes binaries with args/stdin
- supports single-process and multi-process scenarios
- returns output, exit code, duration, and pass/fail signals

4. Static Scanning
- parses C files with tree-sitter
- detects banned function usage
- includes file/line/column/snippet evidence

5. Analysis
- similarity fingerprinting and pair ranking
- AI dictionary fingerprint matching and scoring

6. Reporting
- aggregates per-submission and run summary stats
- exports JSON and CSV reports

## Public API Surface (`pkg/engine`)

- `NewRunner(policy, opts...)`
- `Runner.Run(ctx, root, callbacks)`
- `Runner.Cleanup()`
- compile options:
  - `WithWorkers(n)`
  - `WithOutputDir(dir)`
  - `WithShortNames(enabled)`
- `NewExecutorWithOptions(policy, binaryDir, shortNames)`
- `ComputeSimilarityForProcess(submissions, srcFile, cfg)`
- `ComputeAIDetectionForProcess(submissions, srcFile, dict, cfg)`

## Runtime Compatibility

The engine keeps compatibility with current filesystem conventions under:
- `~/.config/autoscan/libraries`
- `~/.config/autoscan/test_files`
- `~/.config/autoscan/expected_outputs`
- `~/.config/autoscan/banned.yaml`
