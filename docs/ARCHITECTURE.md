# Architecture (Target)

## Purpose
Provide a single, testable grading core consumed by multiple UIs.

## Design Principles

1. UI-agnostic engine APIs
2. deterministic outputs for the same inputs
3. explicit data contracts between engine and clients
4. clear package boundaries for maintainability

## High-Level Components

1. **Discovery**
- Traverse submission roots
- Normalize submission identifiers and source files

2. **Compilation**
- Build commands from policy
- Compile in parallel with worker limits
- Emit structured compile results

3. **Execution**
- Execute binaries with args/stdin
- Support test cases and multi-process scenarios
- Return output, exit code, duration, and pass/fail signals

4. **Static Scanning**
- Parse C files
- Detect banned function usage
- Attach file/line references

5. **Analysis**
- Similarity detection
- AI-pattern detection

6. **Reporting**
- Aggregate per-submission and run summaries
- Provide export-ready structures

## API Surface (Proposed)

A small facade package (example):

- `NewSession(policy, options)`
- `Run(rootPath)`
- `Execute(submissionID, executionRequest)`
- `ComputeSimilarity(processName)`
- `ComputeAIDetection(processName)`

The exact shape will be decided during extraction, but the contract should be stable and versioned.
