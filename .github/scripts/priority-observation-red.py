#!/usr/bin/env python3
"""Prepare a complete immutable production checkout with regression tests only.

Never transplant production functions or accept compile failures as red tests.
The test files use existing APIs plus reflection for additive request fields.
"""
import os
from pathlib import Path
import shutil
import subprocess

BASE = '00788a4705152cb90aab089d352a494531e4664b'
old = Path(os.environ['RUNNER_TEMP']) / 'priority-old'
subprocess.run(['git', 'worktree', 'add', '--detach', str(old), BASE], check=True)
for package in ['service', 'handler']:
    file = Path(f'backend/internal/{package}/priority_observation_baseline_test.go')
    shutil.copyfile(file, old / file)
print(f'Exact old production source: {BASE}; only two new regression test files added.')
