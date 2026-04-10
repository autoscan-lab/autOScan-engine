# Cloud Setup

## Overview

The cloud service wraps `autoscan-bridge` with a FastAPI app and is intended to
run privately on Fly.io with assignment configs stored in Cloudflare R2.

Main files:

- `Dockerfile`
- `fly.toml`
- `service/main.py`
- `service/bridge.py`
- `service/storage.py`
- `service/config.py`
- `.env.example`

## Environment

Required variables:

- `R2_ACCOUNT_ID`
- `R2_ACCESS_KEY_ID`
- `R2_SECRET_ACCESS_KEY`
- `R2_BUCKET_NAME`

The service uses:

- `/data` as its runtime directory
- `/data/current` as the active assignment config
- `autoscan-bridge` as the subprocess binary

## Assignment Layout in R2

Expected object layout:

```text
assignments/
  lab1/
    policy.yml
    banned.yaml                # optional
    libraries/                 # optional
    test_files/                # optional
    expected_outputs/          # optional
```

## Local Run

```bash
python3 -m venv .venv
source .venv/bin/activate
pip install -r service/requirements.txt
uvicorn service.main:app --reload --port 8080
```

## HTTP Endpoints

- `GET /health`
- `POST /setup/{assignment}`
- `POST /grade`

Examples:

```bash
curl http://localhost:8080/health
```

```bash
curl -X POST http://localhost:8080/setup/lab1
```

```bash
curl -X POST http://localhost:8080/grade \
  -F "file=@submissions.zip"
```

## Fly.io

The included `fly.toml` is configured for:

- private service on port `8080`
- mounted volume at `/data`
- `mad` primary region
- auto-start / auto-stop enabled

Intended private URL pattern:

```text
http://autoscan-engine-service.flycast:8080
```
