"""Filesystem and R2 helpers for assignment setup and grading inputs."""

from __future__ import annotations

import os
import shutil
import zipfile
from pathlib import Path

import boto3

from service.config import (
    CURRENT_CONFIG_DIR,
    DATA_DIR,
    POLICY_FILE_NAME,
    ServiceError,
    required_env,
)


def ensure_active_config() -> None:
    if not CURRENT_CONFIG_DIR.exists():
        raise ServiceError("No active assignment configured", status_code=503)
    if not (CURRENT_CONFIG_DIR / POLICY_FILE_NAME).exists():
        raise ServiceError(f"Active config is missing {POLICY_FILE_NAME}", status_code=503)


def setup_assignment(assignment: str) -> dict[str, str | int]:
    staging_dir = DATA_DIR / f".staging-{assignment}"
    if staging_dir.exists():
        shutil.rmtree(staging_dir)
    staging_dir.mkdir(parents=True, exist_ok=True)

    prefix = f"assignments/{assignment}/"
    keys = download_assignment(prefix, staging_dir)
    if not keys:
        shutil.rmtree(staging_dir, ignore_errors=True)
        raise ServiceError(f"No files found for assignment '{assignment}'", status_code=404)

    policy_path = staging_dir / POLICY_FILE_NAME
    if not policy_path.exists():
        shutil.rmtree(staging_dir, ignore_errors=True)
        raise ServiceError(
            f"Missing {POLICY_FILE_NAME} in assignment '{assignment}'",
            status_code=400,
        )

    activate_staging_dir(staging_dir)
    return {
        "assignment": assignment,
        "files_downloaded": len(keys),
        "config_dir": str(CURRENT_CONFIG_DIR),
    }


def extract_zip(archive_path: Path, target_dir: Path) -> None:
    target_dir.mkdir(parents=True, exist_ok=True)
    try:
        with zipfile.ZipFile(archive_path) as archive:
            safe_extract(archive, target_dir)
    except zipfile.BadZipFile as exc:
        raise ServiceError("Uploaded file is not a valid zip archive", status_code=400) from exc


def safe_extract(archive: zipfile.ZipFile, target_dir: Path) -> None:
    target_root = target_dir.resolve()
    for member in archive.infolist():
        member_path = (target_dir / member.filename).resolve()
        if target_root != member_path and target_root not in member_path.parents:
            raise ServiceError("Zip archive contains an invalid path", status_code=400)
    archive.extractall(target_dir)


def activate_staging_dir(staging_dir: Path) -> None:
    DATA_DIR.mkdir(parents=True, exist_ok=True)

    backup_dir = DATA_DIR / ".previous"
    if backup_dir.exists():
        shutil.rmtree(backup_dir)

    if CURRENT_CONFIG_DIR.exists():
        os.rename(CURRENT_CONFIG_DIR, backup_dir)

    try:
        os.rename(staging_dir, CURRENT_CONFIG_DIR)
    except Exception:
        if backup_dir.exists() and not CURRENT_CONFIG_DIR.exists():
            os.rename(backup_dir, CURRENT_CONFIG_DIR)
        raise
    else:
        if backup_dir.exists():
            shutil.rmtree(backup_dir)


def download_assignment(prefix: str, destination: Path) -> list[str]:
    bucket_name = required_env("R2_BUCKET_NAME")
    client = boto3.client(
        "s3",
        endpoint_url=f"https://{required_env('R2_ACCOUNT_ID')}.r2.cloudflarestorage.com",
        aws_access_key_id=required_env("R2_ACCESS_KEY_ID"),
        aws_secret_access_key=required_env("R2_SECRET_ACCESS_KEY"),
        region_name="auto",
    )

    paginator = client.get_paginator("list_objects_v2")
    downloaded_keys: list[str] = []

    for page in paginator.paginate(Bucket=bucket_name, Prefix=prefix):
        for item in page.get("Contents", []):
            key = item["Key"]
            if key.endswith("/"):
                continue

            relative_path = Path(key.removeprefix(prefix))
            local_path = destination / relative_path
            local_path.parent.mkdir(parents=True, exist_ok=True)
            client.download_file(bucket_name, key, str(local_path))
            downloaded_keys.append(key)

    return downloaded_keys
