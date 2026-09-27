#!/usr/bin/env python3
"""Exercise a running gateway, its saved account selection, and the BPS upstream.

Set BPS_E2E_BASE_URL, BPS_E2E_API_KEY, and optionally BPS_E2E_MODEL.
The caller selects and configures the test account(s) through the admin UI/API.
This script never changes account settings or renders, decodes, or uploads an
actual image. Its inert image part must be rejected or stripped locally.
Successful probes use the selected real upstream and can consume tokens.
"""

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--expect", choices=("reject", "ignore"), required=True)
    parser.add_argument("--stream", action="store_true")
    parser.add_argument("--output-index", type=int, default=1)
    parser.add_argument("--image-chars", type=int, default=128)
    args = parser.parse_args()
    if not 1 <= args.output_index <= 5000:
        parser.error("--output-index must be between 1 and 5000")
    if not 32 <= args.image_chars <= 10_000_000:
        parser.error("--image-chars must be between 32 and 10000000")
    base = os.environ.get("BPS_E2E_BASE_URL", "").rstrip("/")
    key = os.environ.get("BPS_E2E_API_KEY", "")
    parsed = urllib.parse.urlsplit(base)
    if parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username or parsed.query or parsed.fragment:
        parser.error("set BPS_E2E_BASE_URL to the gateway API base, including /v1")
    if not key:
        parser.error("set BPS_E2E_API_KEY to a key using the intended BPS account group")

    marker = "BPS_IGNORE_IMAGES_OK"
    prefix = "data:image/png;base64,"
    # Invalid base64 intentionally prevents visual processing if misconfigured.
    inert_image = prefix + "!" * (args.image_chars - len(prefix))
    history = [{"role": "user", "content": "."} for _ in range(args.output_index - 1)]
    history.extend([
        {"type": "custom_tool_call", "call_id": "image_repro", "name": "exec", "input": "echo transport-check"},
        {"type": "custom_tool_call_output", "call_id": "image_repro", "output": [
            {"type": "input_text", "text": "transport-check"},
            {"type": "input_image", "image_url": inert_image, "detail": "original"},
        ]},
        {"role": "user", "content": "Continue the text-only transport check."},
    ])
    payload = {
        "model": os.environ.get("BPS_E2E_MODEL", "gpt-6-astra"),
        "stream": args.stream, "reasoning": {"effort": "low"},
        "instructions": f"Reply exactly {marker}. Do not call tools.",
        "tools": [{"type": "custom", "name": "exec"}], "input": history,
    }
    raw = json.dumps(payload).encode()
    request = urllib.request.Request(base + "/responses", raw, {
        "Authorization": "Bearer " + key, "Content-Type": "application/json",
        "session_id": "bps-image-e2e-" + uuid.uuid4().hex,
    })
    started = time.monotonic()
    try:
        with urllib.request.build_opener(NoRedirect).open(request, timeout=180) as response:
            status, body = response.status, response.read().decode()
    except urllib.error.HTTPError as error:
        status, body = error.code, error.read().decode()
    result = {}
    if args.stream and status == 200:
        for line in body.splitlines():
            if not line.startswith("data: ") or line[6:].strip() == "[DONE]":
                continue
            event = json.loads(line[6:])
            if event.get("type") in ("response.completed", "response.failed", "response.incomplete"):
                result = event.get("response", {})
    else:
        result = json.loads(body)
    error = result.get("error") or {}
    output_text = "".join(
        part.get("text", "") for item in result.get("output", [])
        for part in item.get("content", []) if isinstance(part, dict)
    )
    expected_path = f"path=input[{args.output_index}].output[1]"
    if args.expect == "reject":
        passed = status == 400 and error.get("code") == "basispoints_request_invalid" and expected_path in error.get("message", "")
    else:
        passed = status == 200 and result.get("status") == "completed" and output_text.strip() == marker
    print(json.dumps({
        "passed": passed, "expect": args.expect, "http_status": status,
        "response_status": result.get("status"), "stream": args.stream,
        "input_items": len(history), "image_chars": args.image_chars,
        "image_path": expected_path, "request_bytes": len(raw),
        "marker_received": output_text.strip() == marker,
        "error_code": error.get("code"), "seconds": round(time.monotonic() - started, 2),
    }))
    return 0 if passed else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError) as error:
        # Do not print URLs, credentials, request bodies or upstream text.
        print(json.dumps({"passed": False, "error_type": type(error).__name__}))
        sys.exit(1)
