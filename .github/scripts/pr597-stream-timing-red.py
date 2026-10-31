"""Dedicated CI only: retain new tests, substitute the reviewed production file."""

import json
from pathlib import Path
import re
import subprocess
import sys


ORIGINAL_HEAD = "e7b6e505560fbe810c36c2bbd50b57b1f437ff49"
ROOT = Path(__file__).resolve().parents[2]
TARGET = ROOT / "backend/internal/service/openai_sse_json_documents.go"
TESTS = ROOT / "backend/internal/service/openai_sse_timing_context_test.go"


def main():
    log = Path(sys.argv[1])
    stderr_log = log.with_suffix(".stderr.log")
    current_source = TARGET.read_bytes()
    current_tests = TESTS.read_bytes()
    original_source = subprocess.check_output(
        ["git", "show", f"{ORIGINAL_HEAD}:{TARGET.relative_to(ROOT)}"], cwd=ROOT
    )
    try:
        TARGET.write_bytes(original_source)
        with log.open("w") as output, stderr_log.open("w") as errors:
            result = subprocess.run(
                [
                    "go", "test", "-json", "-tags=unit", "./internal/service",
                    "-run", "^Test(OpenAISSEScannerTimingEventContext|OpenAINativeSSETimingEventContext)$",
                    "-count=1", "-timeout=5m",
                ],
                cwd=ROOT / "backend", stdout=output, stderr=errors,
                check=False,
            )
    finally:
        TARGET.write_bytes(current_source)
        if TESTS.read_bytes() != current_tests:
            raise RuntimeError("RED must retain the new regression tests unchanged")

    raw = log.read_text()
    stderr = stderr_log.read_text()
    print(raw, end="")
    print(stderr, end="", file=sys.stderr)
    if result.returncode != 1:
        raise RuntimeError(f"RED expected test exit 1, got {result.returncode}")
    if any(marker in raw + stderr for marker in ("[build failed]", "undefined:", "syntax error:", "no tests to run")):
        raise RuntimeError("RED must be a specific assertion failure, not a build/selection failure")
    records = [json.loads(line) for line in raw.splitlines()]
    failures = {record.get("Test") for record in records if record.get("Action") == "fail"}
    passes = {record.get("Test") for record in records if record.get("Action") == "pass"}
    scanner_test = "TestOpenAISSEScannerTimingEventContext/event_without_json_type/fragmented=false/scan=2"
    scanner_output = "".join(record.get("Output", "") for record in records if record.get("Test") == scanner_test)
    if not (
        scanner_test in failures
        and "SSE visible stage must use the current event context" in scanner_output
        and re.search(r"expected\s*:\s*true", scanner_output)
        and re.search(r"actual\s*:\s*false", scanner_output)
    ):
        raise RuntimeError("RED missing the expected missing-event-type visibility assertion")
    for interval in (0, 30):
        native_test = f"TestOpenAINativeSSETimingEventContext/interval={interval}/json_type=false"
        native_output = "".join(record.get("Output", "") for record in records if record.get("Test") == native_test)
        if not (
            native_test in failures
            and "sse_visible_first" in native_output
            and "native scanner must record the SSE visible stage on the response attempt" in native_output
            and "does not contain" in native_output
        ):
            raise RuntimeError(f"RED missing the native handler assertion for interval={interval}")
        if f"TestOpenAINativeSSETimingEventContext/interval={interval}/json_type=true" not in passes:
            raise RuntimeError("RED must preserve the existing JSON-type native timing behavior")
    print("RED verified: missing SSE visible stage assertions; production file restored for GREEN.")


if __name__ == "__main__":
    main()
