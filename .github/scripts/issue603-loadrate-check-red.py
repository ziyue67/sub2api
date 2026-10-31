#!/usr/bin/env python3
"""Accept only the named behavioral counterexample for Issue #603 RED."""

from pathlib import Path
import re
import sys


log = Path(sys.argv[1]).read_text(encoding="utf-8", errors="replace")
if "--- FAIL: TestBuildOpenAIAccountLoadRequestUsesRealConcurrency" not in log:
    raise SystemExit("missing expected RED behavior test failure")
if not re.search(r"MaxConcurrency:\s*10000", log):
    raise SystemExit("missing expected configured-factor capacity value in assertion diff")
for marker in ("build failed", "undefined:", "no tests to run", "no test files"):
    if marker in log:
        raise SystemExit(f"invalid RED run: {marker}")
print("verified expected capacity behavior failure on baseline source")
