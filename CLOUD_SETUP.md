# Deploy autOScan-engine

## 1. R2 bucket layout

```
your-bucket-name/
  banned.yaml
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
- `POST /grade` — accepts a zip, returns JSON results
