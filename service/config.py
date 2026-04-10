"""Shared configuration for the engine service."""

from __future__ import annotations

import os
from pathlib import Path

DATA_DIR = Path("/data")
CURRENT_CONFIG_DIR = DATA_DIR / "current"
POLICY_FILE_NAME = "policy.yml"
BRIDGE_BIN = "autoscan-bridge"


class ServiceError(RuntimeError):
    """Simple service error carrying an HTTP status code."""

    def __init__(self, message: str, status_code: int = 500):
        super().__init__(message)
        self.status_code = status_code


def required_env(name: str) -> str:
    value = os.environ.get(name)
    if value:
        return value
    raise ServiceError(f"Missing required environment variable: {name}")
