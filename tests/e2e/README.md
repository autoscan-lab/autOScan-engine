# Local engine stack and end-to-end tests

Runs the real engine image locally, the same Dockerfile Fly builds, with
[SeaweedFS](https://github.com/seaweedfs/seaweedfs) standing in for R2. Use it to
try engine changes before deploying and to catch regressions in grading,
terminals, and the HTTP contract the web app depends on.

Requires Docker (Desktop) and Go. Nothing here touches production R2, Fly, or MongoDB.

## Run

```bash
docker compose -f tests/e2e/compose.yml up -d --build
go test -tags e2e ./tests/e2e/ -v -count=1
docker compose -f tests/e2e/compose.yml down
```

Rebuild after changing engine code (`up -d --build` again). The suite seeds the
bucket on every run, so it can run repeatedly against the same stack. Plain
`go test ./...` (and CI) skips it: every file carries the `e2e` build tag.

| Test | Checks |
|---|---|
| `TestGradeProducesPassingResults` | a grade job downloads the policy and submissions and writes a passing `result.json` |
| `TestTerminalGetsTheRunsOwnPolicyFiles` | a terminal gets the libraries and test files of its own run, not of the last graded assignment |
| `TestSolutionTerminalBuildsFromThePolicy` | a solution terminal gets the assignment's solution, libraries, and test files, with the solution already built and runnable |
| `TestSolutionTerminalThatFailsToCompileSaysWhy` | a solution that doesn't compile closes the terminal with the compile error as the reason |
| `TestSolutionTerminalWithoutSolutionCloses` | a solution terminal for an assignment with no solution files closes instead of opening an empty shell |
| `TestGradesQueueWithoutBlockingTerminals` | a second grade waits as `Queued` while terminals still open instantly |
| `TestCancelQueuedGrade` | `DELETE /grade/{id}` cancels a job that is still queued |
| `TestProgressBurstIsNotRateLimited` | many progress polls at once are never rejected |
| `TestTerminalKeystrokeEcho` | logs keystroke echo latency through the engine (network excluded) |

## What's in the stack

- `engine` on `http://localhost:18080`, secret `local-test-secret`, bucket
  `autoscan-local`, pointed at the S3 store through `R2_ENDPOINT`. It runs
  privileged so bubblewrap can sandbox, as on Fly.
- `s3` (SeaweedFS) on `http://localhost:19000`, access key `local-access-key`,
  secret `local-secret-key` (see `s3.json`).
- `testdata/bucket` is copied to the bucket root: `banned.yaml` and two
  assignments, `S2_BC` and `S2_AICE`, whose library and test file names differ
  so a terminal shows which policy it got. Only `S2_BC` has a `solution/`.
- `testdata/submissions/<name>` is zipped to `web/uploads/staging/<name>.zip`:
  `fast` passes its test, `slow` sleeps so a grade stays busy for a few seconds.

## Adding a scenario

Add a policy under `testdata/bucket/assignments/<NAME>/` (same layout as R2:
`policy.yml`, `libraries/`, `test_files/`, `expected_outputs/`) or a submissions
folder under `testdata/submissions/`, then a `Test...` in `e2e_test.go` using the
helpers there: `gradeDone`, `startGrade`, `waitRun`, `waitStage`, `mintToken`,
`terminalRun`, `readJSON`.

## Poking at it by hand

Everything the web app would call works against `localhost:18080` with the
`X-Autoscan-Secret: local-test-secret` header, e.g.
`curl -H 'X-Autoscan-Secret: local-test-secret' localhost:18080/progress/<run_id>`.
Engine logs: `docker compose -f tests/e2e/compose.yml logs -f engine`.

The web app is not wired to this stack: its R2 client has no endpoint
override, browser uploads would need CORS on the local store, and it would
need its own MongoDB database.
