# Deploy autOScan-engine

The additive AI evidence contract and engine-first rollout are documented in
[AI_DETECTION.md](AI_DETECTION.md). The dictionary is optional for style and
provenance analysis.

## 1. R2 bucket layout

```
your-bucket-name/
  banned.yaml             # global, applied to every assignment
  ai_dictionary.yaml      # global, optional — without it AI detection skips dictionary patterns
  assignments/
    <assignment-name>/    # one folder per assignment; the whole prefix is synced
      policy.yml
      expected_outputs/   # optional: test-case + scenario expected outputs
      libraries/          # optional: companion .c/.o/.h files
      test_files/         # optional: data files passed as args; Linux executables
                          #   here are made runnable when copied
      solution/           # optional: reference solution for solution terminals
    .../
      policy.yml
```

## 2. Create .env

```
R2_ACCOUNT_ID=your-cloudflare-account-id
R2_ACCESS_KEY_ID=your-r2-access-key-id
R2_SECRET_ACCESS_KEY=your-r2-secret-access-key
R2_BUCKET_NAME=your-bucket-name
ENGINE_SECRET=your-shared-secret
```

`R2_ENDPOINT` (optional) points the engine at another S3-compatible store, e.g.
the SeaweedFS store in the local stack ([tests/e2e](tests/e2e/README.md)).
`PORT` (default `8080`) and `AUTOSCAN_DATA_DIR` (default `/data`) are optional too.

### Automatic AI reanalysis

On startup after deployment, the engine refreshes saved runs whose
`ai_detection.method` differs from `contextual-v2`. It scans `web/runs/` by
default; set `R2_APP_PREFIX` to the frontend's prefix, or
`AUTOSCAN_AI_RESULT_PREFIX` to its complete result prefix, if customized.
No manual submission reruns are needed. Merging into `dev` does not deploy;
the refresh starts when the new engine is deployed from `main` and starts.

The worker reconstructs sources from each saved `result.json`, recomputes
style, scores and peer statistics for the whole run, and changes only
`ai_detection` plus `ai_reanalysis` provenance. It preserves grading, tests,
similarity, original dictionary matches and archive-only tool evidence.
The student programs are never compiled or executed. Original uploads and
local grading workspaces are not needed. Missing saved sources remain failed
and visible in progress rather than receiving invented scores.

Before updating, the previous result is backed up under
`<prefix>/<run-id>/ai-revisions/contextual-v2/<content-sha256>.json`.
Conditional object writes prevent overwriting concurrent updates or recreating
deleted runs. Successful method stamps are durable checkpoints. The worker
retries failures and rescans every five minutes while running, resumes on
restart, shares the grading queue, and counts as active work for idle exit.
Per-pass progress is saved at
`/data/ai-reanalysis/contextual-v2/status.json`; inspect the authenticated
`GET /ai-reanalysis` endpoint for method, state, scanned, updated, current,
failed and last error. Bump `domain.AIDetectionMethod` whenever future detector
changes require refreshing saved runs. Backups are excluded from scanning.

The app reads refreshed results at the existing keys. AI-history cache entries
expire within 60 seconds; reopening or refreshing a run reads its latest report.

## 3. Deploy

```bash
# One-time: provisions the persistent disk declared in fly.toml
fly volumes create autoscan_engine_data --region cdg --size 1

fly secrets import < .env
fly deploy
```

The machine is not auto-stopped by Fly's proxy (it can't see background grade
jobs). Instead the server exits itself after `AUTOSCAN_IDLE_EXIT` (set in
`fly.toml`) with no requests, grade jobs, or terminal sockets, and the proxy
starts it again on the next request. Leave the variable unset locally to never exit.

## Endpoints

- `GET  /health`
- `GET  /ai-reanalysis` - authenticated automatic AI backfill progress
- `POST /grade` - async grading: form fields `assignment`, `r2_key`,
  `result_key_prefix`, `export_key_prefix`, and optional `callback_url`; returns
  `202 { run_id }` and writes `<result_key_prefix>/<run_id>/result.json` when done.
  Once the run settles (done, failed or cancelled) the engine POSTs
  `{ "run_id": … }` to `callback_url` with the `X-Autoscan-Secret` header,
  retrying network errors and 5xx a few times; the receiver reads the outcome
  from `GET /progress/{run_id}`. Jobs run one at a time;
  later ones report the `Queued` stage until their turn.
- `DELETE /grade/{run_id}` - cancels a queued or running grade job (404 once finished)
- `GET  /progress/{token}` - `{ fraction, stage, state, detail? }` for a grade run id
  or a sandbox progress token
- `POST /sandbox/analyze` - ad-hoc similarity + AI detection on a zip, no run state; waits for a running grade and returns 409 if it is still busy after 3 minutes
- `GET  /terminal` - WebSocket, one connection per shell pane; auth is a
  short-lived HMAC token minted by the web app (not the secret header). Panes
  of one token share a sandboxed session. A token names either a graded
  submission (`run_id`, `submission_id`) or, with `solution: true`, an
  assignment whose `solution/` files are copied in with its libraries and test
  files and compiled before the shell opens. A solution that doesn't compile
  closes the socket with the compile error as the reason. Binary frames carry
  terminal I/O. The client sends `{"type":"resize","cols","rows"}` text frames,
  and the engine sends `{"type":"processes","processes":[{"pid","name"}]}`
  whenever the pane's foreground job changes. The PIDs are as the sandbox's
  shells see them, and the list is empty at the prompt.

Grade and sandbox AI submissions expose optional `ai_score` (0–100 heuristic,
method version, named point contributions), alongside `style`, `tells`,
`token_metrics`, and `similar_to_flagged`. `best_score` remains dictionary
coverage. Style features include bounded named-file source ranges and the
full occurrence count; contributions reference them with `feature_key`.
Other contributions carry their own ranges. Tells can include `end_line`.
Missing overall scores indicate insufficient evidence, not zero. The review
rules and scoring floors are documented in [AI_DETECTION.md](AI_DETECTION.md).

Each grade run downloads its assignment from R2 into `runs/<run_id>/config` on
the volume and keeps it with the run's workspace, so a run's terminal always gets
the libraries and test files it was graded with. Runs are never deleted by age:
only once the volume passes 90% full are the oldest removed (never the newest). The terminal token
also carries the assignment name, which the engine uses to fetch a run's config
when it has none.

### Global banned-code constructs

`banned.yaml` accepts a `constructs` mapping with `variable_length_arrays`,
`initialized_arrays`, and `pthread_attributes` booleans alongside the existing
`banned` function list. Each defaults to true when omitted; false disables that
scanner rule. Hits retain the existing function/file/line/column/snippet JSON
shape, with a construct label in `function`. See [AI_DETECTION.md](AI_DETECTION.md)
for recognition boundaries and the independent AI review rules. Deploy the
engine before the app's construct-policy controls; old run results remain valid.
