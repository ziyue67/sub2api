#!/usr/bin/env python3
"""Require the six intended behavioral failures, never failed setup/builds."""
from pathlib import Path
import re
import sys

raw = Path(sys.argv[1]).read_text()
assert not re.search(r'build failed|undefined:|syntax error|setup failed|panic:', raw, re.I), 'red run failed before behavioral assertions'
expected = {
 'Endpoint': ('actual Responses ingress must survive CC capability fallback', 'expected: 1', 'actual  : 0'),
 'History': ('persisted requested_model must be read with the public identity', 'public-alias', 'channel-route'),
 'Mapping': ('forward and scheduler identities must normalize the same mapping', '"route-model"', '" route-model "'),
 'Freshness': ('an empty completed read must not hide new-account persistence for 30 seconds', '"30s" is not less than or equal to "5s"'),
 'Sticky': ('ordinary sticky cannot bypass a current measured penalty', 'expected: 2', 'actual  : 1'),
 'HardStop': ('all matching accounts are stopped; prohibited capacity must never be used', 'Expected nil, but got:'),
}
failed = set(re.findall(r'^--- FAIL: TestPriorityObservationBaseline(\w+)\b', raw, re.M))
assert failed == set(expected), f'wrong behavioral failures: {failed}'
for name, evidence in expected.items():
    block = re.search(r'^=== RUN   TestPriorityObservationBaseline' + name + r'\n(.*?)^--- FAIL: TestPriorityObservationBaseline' + name + r'\b', raw, re.M | re.S)
    assert block is not None, f'missing test output: {name}'
    normalized = re.sub(r'[ \t]+', ' ', block[1])
    for value in evidence:
        assert re.sub(r'[ \t]+', ' ', value) in normalized, f'{name} failed for an unexpected reason; missing {value!r}'
    print(f'Confirmed exact old-code behavior: {name}')
