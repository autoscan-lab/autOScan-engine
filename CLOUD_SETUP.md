# Deploy autOScan-engine

## 1. R2 bucket layout

```
your-bucket-name/
  banned.yaml             # global, applied to every assignment
  ai_dictionary.yaml      # global, optional — required only for AI-detection
  assignments/
    S0/
      policy.yml
    S1/
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

## 3. Deploy

```bash
# One-time: provisions the persistent disk declared in fly.toml
fly volumes create autoscan_engine_data --region ams --size 1

fly secrets import < .env
fly deploy
```

## Endpoints

- `GET  /health`
- `POST /setup/{assignment}` — loads policy from R2 (e.g. `/setup/S0`)
- `POST /grade` — accepts a zip, returns JSON results + `run_id`
- `POST /analyze/similarity` — computes similarity for an existing `run_id`
- `POST /analyze/ai-detection` — computes AI detection for an existing `run_id`

`/grade` only performs grading and stores run metadata for follow-up analysis.

`/analyze/similarity` and `/analyze/ai-detection` accept JSON body:

```json
{
  "run_id": "9be7e2b7718a5be4634de0db",
  "include_spans": false,
  "top_k": 25
}
```

- `run_id` is required and must come from a previous `/grade` response.
- `include_spans` defaults to `false` (summary payloads only).
- `top_k` is optional; when set, trims the response to the top N entries.
