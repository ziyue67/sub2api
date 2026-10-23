#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"

python3 - <<'PY'
import json
import os
from pathlib import Path
import subprocess
import tempfile

key = "SERVER_TRUSTED_PROXIES"
proxy_list = "127.0.0.1/32,::1/128,172.18.0.1/32"
shell_list = "192.0.2.10/32"
sample = Path("deploy/.env.example").read_text()

# Render the actual templates; no containers, database or network are started.
# None represents an unset variable, which must remain distinct from "".
cases = (
    ("unset", "", None, None),
    ("sample defaults", sample, None, None),
    ("dotenv list", f"{key}={proxy_list}\n", None, proxy_list),
    ("dotenv empty", f"{key}=\n", None, ""),
    ("shell list", "", proxy_list, proxy_list),
    ("shell empty", "", "", ""),
    ("shell overrides dotenv", f"{key}={proxy_list}\n", shell_list, shell_list),
    ("shell empty overrides dotenv", f"{key}={proxy_list}\n", "", ""),
)

with tempfile.TemporaryDirectory(prefix="compose-trusted-proxies-") as tmp:
    env_file = Path(tmp) / ".env"
    for filename in (
        "docker-compose.yml",
        "docker-compose.local.yml",
        "docker-compose.standalone.yml",
        "docker-compose.dev.yml",
    ):
        path = Path("deploy") / filename
        for name, env_text, shell_value, expected in cases:
            env_file.write_text(env_text)
            env = dict(
                os.environ, POSTGRES_PASSWORD="compose-test-password",
                DATABASE_HOST="postgres", DATABASE_PASSWORD="compose-test-password",
                REDIS_HOST="redis",
            )
            env.pop(key, None)
            if shell_value is not None:
                env[key] = shell_value
            result = subprocess.run(
                ["docker", "compose", "--env-file", str(env_file), "-f", str(path),
                 "config", "--format", "json"],
                env=env, check=True, capture_output=True, text=True,
            )
            actual = json.loads(result.stdout)["services"]["sub2api"]["environment"].get(key)
            assert actual == expected, f"{path} ({name}): {actual!r} != {expected!r}"

print("docker compose trusted proxy environment test passed")
PY
