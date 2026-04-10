"""FastAPI entrypoint for the engine service."""

from __future__ import annotations

import logging
import tempfile
from pathlib import Path

from fastapi import FastAPI, File, HTTPException, UploadFile

from service.bridge import run_grading_pipeline
from service.config import ServiceError
from service.storage import ensure_active_config, extract_zip, setup_assignment

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
)
logger = logging.getLogger(__name__)

app = FastAPI(
    title="autOScan-engine-service",
    description="HTTP wrapper for autoscan-bridge",
    version="0.1.0",
)


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok"}


@app.post("/setup/{assignment}")
def setup_assignment_route(assignment: str) -> dict[str, str | int]:
    try:
        result = setup_assignment(assignment)
    except ServiceError as exc:
        raise HTTPException(status_code=exc.status_code, detail=str(exc)) from exc

    logger.info("Activated assignment %s with %d files", assignment, result["files_downloaded"])
    return {"status": "ok", **result}


@app.post("/grade")
async def grade_submission(file: UploadFile = File(...)) -> dict:
    try:
        ensure_active_config()
    except ServiceError as exc:
        raise HTTPException(status_code=exc.status_code, detail=str(exc)) from exc

    suffix = Path(file.filename or "submissions.zip").suffix or ".zip"

    with tempfile.TemporaryDirectory(prefix="autoscan-grade-") as temp_dir:
        temp_path = Path(temp_dir)
        archive_path = temp_path / f"upload{suffix}"
        workspace_dir = temp_path / "workspace"

        contents = await file.read()
        archive_path.write_bytes(contents)

        try:
            extract_zip(archive_path, workspace_dir)
            response = run_grading_pipeline(workspace_dir)
        except ServiceError as exc:
            raise HTTPException(status_code=exc.status_code, detail=str(exc)) from exc

        logger.info("Processed grading request with %d submissions", len(response["results"]))
        return response
