# Migration Plan (autOScan -> autOScan-engine)

## Objective
Move shared grading logic into this repository with zero behavior drift.

## Phase 1: Baseline and Contracts

1. Define public engine interfaces and result models.
2. Add smoke tests/fixtures for core flows to lock current behavior.
3. Document expected inputs and outputs.

## Phase 2: Engine Extraction

1. Extract domain models and policy parsing.
2. Extract discovery, compiler, scanner, executor, and analysis modules.
3. Keep package-level APIs compatible with current TUI usage where possible.

## Phase 3: TUI Adoption

1. Update current `autOScan` TUI to consume this engine.
2. Use local module replacement during development.
3. Validate output parity with current runs.

## Phase 4: Versioning and Release

1. Tag `v0.1.0` after TUI parity is confirmed.
2. Pin TUI to tagged engine version.
3. Establish semantic versioning policy.

## Validation Checklist

- compile/run outputs match prior behavior
- banned hits and line references remain consistent
- similarity/AI results are equivalent for same settings
- exports and summary totals are unchanged
