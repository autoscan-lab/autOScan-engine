"""Bridge orchestration for session and test-case grading runs."""

from __future__ import annotations

import json
import logging
import subprocess
from pathlib import Path
from typing import Any

from service.config import BRIDGE_BIN, CURRENT_CONFIG_DIR, POLICY_FILE_NAME, ServiceError

logger = logging.getLogger(__name__)


class InvalidTestCaseIndexError(ServiceError):
    """Raised when a requested test case index is outside the policy range."""


def run_grading_pipeline(workspace_dir: Path) -> dict[str, Any]:
    policy_path = CURRENT_CONFIG_DIR / POLICY_FILE_NAME
    run_events = run_bridge_command(
        "run-session",
        "--workspace",
        str(workspace_dir),
        "--policy",
        str(policy_path),
        "--config-dir",
        str(CURRENT_CONFIG_DIR),
    )

    run_payload = find_event_payload(run_events, "run_complete", "run")
    if run_payload is None:
        raise ServiceError("autoscan-bridge did not emit run_complete")

    raw_results = run_payload.get("submissions", [])
    results_by_id = {
        submission["id"]: {**submission, "tests": empty_test_summary()}
        for submission in raw_results
    }

    submission_ids = [submission["id"] for submission in raw_results]
    if submission_ids:
        populate_test_results(workspace_dir, policy_path, submission_ids, results_by_id)

    summary = run_payload.get("summary", {})
    return {
        "policy_name": summary.get("policy_name"),
        "root": summary.get("root"),
        "started_at": summary.get("started_at"),
        "finished_at": summary.get("finished_at"),
        "duration_ms": summary.get("duration_ms"),
        "summary": summary,
        "results": list(results_by_id.values()),
    }


def populate_test_results(
    workspace_dir: Path,
    policy_path: Path,
    submission_ids: list[str],
    results_by_id: dict[str, dict[str, Any]],
) -> None:
    first_submission_id = submission_ids[0]
    first_cases = collect_test_cases(workspace_dir, policy_path, first_submission_id)
    if not first_cases:
        logger.info("Policy %s defines no test cases", policy_path)
        return

    results_by_id[first_submission_id]["tests"] = summarize_test_cases(first_cases)

    total_test_cases = len(first_cases)
    for submission_id in submission_ids[1:]:
        cases: list[dict[str, Any]] = []
        for index in range(total_test_cases):
            case = run_test_case(workspace_dir, policy_path, submission_id, index)
            if case is None:
                raise ServiceError(
                    f"Missing test case payload for submission {submission_id} index {index}",
                )
            cases.append(case)

        results_by_id[submission_id]["tests"] = summarize_test_cases(cases)


def collect_test_cases(
    workspace_dir: Path,
    policy_path: Path,
    submission_id: str,
) -> list[dict[str, Any]]:
    try:
        first_case = run_test_case(workspace_dir, policy_path, submission_id, 0)
    except InvalidTestCaseIndexError:
        return []

    if first_case is None:
        return []

    cases = [first_case]
    next_index = 1

    while True:
        try:
            case = run_test_case(workspace_dir, policy_path, submission_id, next_index)
        except InvalidTestCaseIndexError:
            return cases

        if case is None:
            raise ServiceError(f"Missing test case payload for index {next_index}")

        cases.append(case)
        next_index += 1


def run_test_case(
    workspace_dir: Path,
    policy_path: Path,
    submission_id: str,
    index: int,
) -> dict[str, Any] | None:
    events = run_bridge_command(
        "run-test-case",
        "--workspace",
        str(workspace_dir),
        "--policy",
        str(policy_path),
        "--config-dir",
        str(CURRENT_CONFIG_DIR),
        "--submission-id",
        submission_id,
        "--test-case-index",
        str(index),
    )

    case_payload = find_event_payload(events, "test_case_complete", "test_case")
    return normalize_case_payload(case_payload) if case_payload else None


def run_bridge_command(*args: str) -> list[dict[str, Any]]:
    command = [BRIDGE_BIN, *args]
    logger.info("Running bridge command: %s", " ".join(command))

    completed = subprocess.run(
        command,
        capture_output=True,
        text=True,
        check=False,
    )

    events = parse_ndjson_events(completed.stdout)
    if completed.returncode == 0:
        return events

    error_payload = find_event_payload(events, "error", "message")
    error_message = error_payload or completed.stderr.strip() or "autoscan-bridge failed"
    if "invalid --test-case-index" in error_message:
        raise InvalidTestCaseIndexError(error_message)
    raise ServiceError(error_message)


def parse_ndjson_events(output: str) -> list[dict[str, Any]]:
    events: list[dict[str, Any]] = []
    for raw_line in output.splitlines():
        line = raw_line.strip()
        if not line:
            continue
        try:
            events.append(json.loads(line))
        except json.JSONDecodeError as exc:
            raise ServiceError(f"Invalid bridge output: {exc}") from exc
    return events


def find_event_payload(
    events: list[dict[str, Any]],
    event_type: str,
    payload_key: str,
) -> Any | None:
    for event in reversed(events):
        if event.get("type") == event_type:
            return event.get(payload_key)
    return None


def normalize_case_payload(case_payload: dict[str, Any]) -> dict[str, Any]:
    return {
        "index": case_payload.get("test_case_index"),
        "name": case_payload.get("test_case_name"),
        "status": case_payload.get("status"),
        "exit_code": case_payload.get("exit_code"),
        "duration_ms": case_payload.get("duration_ms"),
        "message": case_payload.get("message"),
        "output_match": case_payload.get("output_match"),
    }


def summarize_test_cases(cases: list[dict[str, Any]]) -> dict[str, Any]:
    summary = empty_test_summary()
    summary["total"] = len(cases)
    summary["cases"] = cases

    for case in cases:
        status = case.get("status")
        if status == "pass":
            summary["passed"] += 1
        elif status == "compile_failed":
            summary["compile_failed"] += 1
        else:
            summary["failed"] += 1

        if case.get("output_match") == "missing":
            summary["missing_expected_output"] += 1

    return summary


def empty_test_summary() -> dict[str, Any]:
    return {
        "total": 0,
        "passed": 0,
        "failed": 0,
        "compile_failed": 0,
        "missing_expected_output": 0,
        "cases": [],
    }
