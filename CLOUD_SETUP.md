# Deploy autOScan-engine

## 1. R2 bucket layout

```
your-bucket-name/
  banned.yaml             # global, applied to every assignment
  ai_dictionary.yaml      # global, optional — required only for AI-detection
  assignments/
    <assignment-name>/    # one folder per assignment; the whole prefix is synced
      policy.yml
      expected_outputs/   # optional: test-case + scenario expected outputs
      libraries/          # optional: companion .c/.o/.h files
      test_files/         # optional: data files passed as args
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

## 3. Deploy

```bash
# One-time: provisions the persistent disk declared in fly.toml
fly volumes create autoscan_engine_data --region ams --size 1

fly secrets import < .env
fly deploy
```

The machine is not auto-stopped by Fly's proxy (it can't see background grade
jobs). Instead the server exits itself after `AUTOSCAN_IDLE_EXIT` (set in
`fly.toml`) with no requests, grade jobs, or terminal sockets, and the proxy
starts it again on the next request. Leave the variable unset locally to never exit.

## Endpoints

- `GET  /health`
- `POST /grade` - async grading: form fields `assignment`, `r2_key`,
  `result_key_prefix`, `export_key_prefix`; returns `202 { run_id }` and writes
  `<result_key_prefix>/<run_id>/result.json` when done. Jobs run one at a time;
  later ones report the `Queued` stage until their turn.
- `DELETE /grade/{run_id}` - cancels a queued or running grade job (404 once finished)
- `GET  /progress/{token}` - `{ fraction, stage, state, detail? }` for a grade run id
  or a sandbox progress token
- `POST /sandbox/analyze` - ad-hoc similarity + AI detection on a zip, no run state
- `GET  /terminal` - WebSocket, one connection per shell pane; auth is a
  short-lived HMAC token minted by the web app (not the secret header). Panes
  of one token share a sandboxed session. A token names either a graded
  submission (`run_id`, `submission_id`) or, with `solution: true`, an
  assignment whose `solution/` files are copied in with its libraries and test
  files.

Each grade run downloads its assignment from R2 into `runs/<run_id>/config` on
the volume and keeps it with the run's workspace, so a run's terminal always gets
the libraries and test files it was graded with. Runs are never deleted by age:
only once the volume passes 90% full are the oldest removed (never the newest). The terminal token
also carries the assignment name, which the engine uses to fetch a run's config
when it has none.
