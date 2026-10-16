#!/usr/bin/env python3
"""One-time migration of the standard systemd sidecar to the built-in manager.

The operator must drain login/2FA tasks first. Downloads are release-pinned;
subsequent application upgrades use the existing built-in runtime manager.
"""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import pwd
import re
import shutil
import subprocess
import tarfile
import tempfile
import time
import urllib.request
import uuid

SYSTEMD_DIR = Path("/etc/systemd/system")
BACKUP_DIR = Path("/var/backups/sub2api-worker")
PROC_DIR = Path("/proc")


def command(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.DEVNULL, timeout=30).strip()


def process_environment(pid):
    return dict(item.split(b"=", 1) for item in
                Path("/proc", pid, "environ").read_bytes().split(b"\0") if b"=" in item)


def application_data_dir(pid, environment):
    configured = environment.get(b"DATA_DIR", b"").strip()
    if configured:
        path = Path(os.fsdecode(configured))
    elif Path("/app/data").is_dir():
        path = Path("/app/data")
    else:
        path = Path("data")
    if not path.is_absolute():
        path = Path("/proc", pid, "cwd").resolve() / path
    return path.resolve()


def property_value(service, name):
    return command("systemctl", "show", service, "--property", name, "--value")


def process_children(pid):
    # Go can start the child from any OS thread, not just the main thread.
    children = set()
    for path in (PROC_DIR / pid / "task").glob("*/children"):
        try:
            children.update(path.read_text().split())
        except FileNotFoundError:
            pass
    return children


def download(url, limit):
    request = urllib.request.Request(url, headers={"User-Agent": "Sub2API-Worker-Migration"})
    with urllib.request.urlopen(request, timeout=120) as response:
        body = response.read(limit + 1)
    if len(body) > limit:
        raise ValueError("release download exceeds size limit")
    return body


def extract_runtime(body, destination):
    """Match the manager's bounded extraction: no links or special files."""
    with tarfile.open(fileobj=io.BytesIO(body), mode="r:gz") as archive:
        members = archive.getmembers()
        total = 0
        seen = set()
        for item in members:
            path = Path(item.name)
            if path.is_absolute() or ".." in path.parts or not (item.isfile() or item.isdir()):
                raise ValueError("unsafe runtime archive")
            if str(path) in seen:
                raise ValueError("duplicate runtime archive entry")
            seen.add(str(path))
            total += item.size
            if total > 768 * 1024 * 1024 or len(seen) > 100000:
                raise ValueError("runtime archive exceeds extraction limit")
        for item in members:
            path = destination / item.name
            if item.isdir():
                path.mkdir(parents=True, exist_ok=True, mode=0o700)
            else:
                path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                with archive.extractfile(item) as source, path.open("xb") as target:
                    shutil.copyfileobj(source, target)
                path.chmod(0o700 if item.mode & 0o111 else 0o600)


def copy_recovery(source, destination):
    if not source.exists():
        return
    if source.is_symlink() or not source.is_dir():
        raise ValueError("recovery source must be a real directory")
    # Never combine journals encrypted under different keys or overwrite history.
    if destination.exists():
        raise ValueError("managed recovery directory already exists; reconcile it before migration")
    for path in source.rglob("*"):
        if path.is_symlink() or not (path.is_file() or path.is_dir()):
            raise ValueError("unsupported recovery entry")
    shutil.copytree(source, destination)
    for path in [destination, *destination.rglob("*")]:
        path.chmod(0o700 if path.is_dir() else 0o600)


def wait_ready(service, root, started):
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        pid = property_value(service, "MainPID")
        if pid != "0" and property_value(service, "ActiveState") == "active":
            # A fresh 2FA claim proves the new child opened its recovery journal
            # and authenticated to this API. Never print the raw journal.
            logs = command("journalctl", "-u", service, "--since", "@" + str(started), "-n", "5000", "-o", "cat", "--no-pager")
            if any('"/api/v1/internal/openai-reauth/totp-claim"' in line and
                   '"status_code": 200' in line for line in logs.splitlines()):
                for child in process_children(pid):
                    try:
                        cmd = (PROC_DIR / child / "cmdline").read_bytes()
                        if str(root).encode() in cmd and b"worker.py" in cmd:
                            return
                    except FileNotFoundError:
                        pass
        time.sleep(1)
    raise RuntimeError("managed worker did not report 2FA readiness")


def migrate(args):
    if os.geteuid() != 0:
        raise ValueError("run as root")
    if not args.drained:
        raise ValueError("drain login/2FA tasks, pause new submissions, then pass --drained")
    import fcntl
    with open("/run/lock/sub2api-deploy.lock", "a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        migrate_locked(args)


def migrate_locked(args):
    service, sidecar = args.service, args.worker_service
    if any(not re.fullmatch(r"[A-Za-z0-9_@.-]+\.service", name) for name in (service, sidecar)) or service == sidecar:
        raise ValueError("invalid service names")
    if property_value(service, "ActiveState") != "active":
        raise ValueError("application must be running before migration")
    pid = property_value(service, "MainPID")
    version_output = command("/proc/" + pid + "/exe", "--version")
    match = re.search(r"Sub2API (\d+\.\d+\.\d+(?:-[\w.-]+)?)\b", version_output)
    if not match:
        raise ValueError("application must be a released build")
    version = match.group(1)
    architecture = {"x86_64": "amd64", "aarch64": "arm64"}.get(os.uname().machine)
    if not architecture:
        raise ValueError("unsupported architecture")
    if "/opt/sub2api-reauth/current/openai_oauth_reauth_worker.py" not in property_value(sidecar, "ExecStart"):
        raise ValueError("only the standard legacy Python sidecar is supported")
    # Inspect only mode names, never source or print environment files.
    worker_pid = property_value(sidecar, "MainPID")
    if worker_pid == "0":
        raise ValueError("legacy sidecar must be running for configuration verification")
    worker_env = process_environment(worker_pid)
    if worker_env.get(b"CODEX_PROTOCOL_ROOT") or worker_env.get(b"TURB_ROOT"):
        raise ValueError("custom email protocol requires an external worker")
    if any(worker_env.get(key) for key in (b"HTTP_PROXY", b"HTTPS_PROXY", b"ALL_PROXY", b"CHATGPT_PROXY_URL",
                                         b"http_proxy", b"https_proxy", b"all_proxy")):
        raise ValueError("custom worker-wide proxy requires manual migration")
    recovery = Path(os.fsdecode(worker_env.get(b"OPENAI_TOTP_JOURNAL_DIR",
                                             b"/var/lib/sub2api-reauth/totp-recovery")))
    if not recovery.is_absolute():
        raise ValueError("legacy recovery path must be absolute")
    account = pwd.getpwnam(property_value(service, "User") or "root")
    data = Path(args.data_dir).resolve()
    app_env = process_environment(pid)
    if data != application_data_dir(pid, app_env):
        raise ValueError("--data-dir must match the running application's persistent directory")
    root = data / "credential-worker"
    target = root / (version + "-linux-" + architecture)
    dropin = SYSTEMD_DIR / (service + ".d") / "90-managed-reauth.conf"
    if root.is_symlink() or dropin.exists() or target.exists() or (root / "totp-recovery").exists():
        raise ValueError("managed migration files already exist; inspect before retrying")
    asset_name = "sub2api-reauth_" + version + "_linux_" + architecture + ".tar.gz"
    release = json.loads(download("https://api.github.com/repos/ranxi2001/sub2api/releases/tags/v" + version, 4 << 20))
    asset = next(item for item in release["assets"] if item["name"] == asset_name)
    digest = asset.get("digest", "")
    if not isinstance(digest, str) or not re.fullmatch(r"sha256:[a-f0-9]{64}", digest):
        raise ValueError("release has no valid runtime SHA-256")
    body = download("https://github.com/ranxi2001/sub2api/releases/download/v" + version + "/" + asset_name, 256 << 20)
    if hashlib.sha256(body).hexdigest() != digest[7:]:
        raise ValueError("runtime checksum mismatch")
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    with tempfile.TemporaryDirectory(prefix=".migrate-", dir=root) as staging:
        runtime = Path(staging) / "runtime"
        runtime.mkdir(mode=0o700)
        extract_runtime(body, runtime)
        if not (runtime / "portable-verified").is_file() or not (runtime / "ubuntu-verified").is_file():
            raise ValueError("runtime is missing portability verification")
        # No task claims or upstream requests. Output is suppressed, including errors.
        env = {"PATH": "/usr/bin:/bin", "LANG": "C.UTF-8", "PYTHONDONTWRITEBYTECODE": "1",
               "OPENAI_REAUTH_WORKER_TOKEN": "migration-check-only-00000000000000000000",
               "TOSUB2_ROOT": str(runtime / "tosub2"), "NODE_EXECUTABLE": str(runtime / "node")}
        subprocess.run([str(runtime / "python"), str(runtime / "worker.py"), "--check"],
                       env=env, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=60)
        (runtime / "ready").write_text(digest[7:])
        (runtime / "ready").chmod(0o600)
        # Re-check the live deployment immediately before touching services.
        if property_value(service, "MainPID") != pid or property_value(sidecar, "MainPID") != worker_pid:
            raise ValueError("deployment changed during preparation")
        enabled = property_value(sidecar, "UnitFileState")
        if enabled not in ("enabled", "disabled"):
            raise ValueError("custom sidecar enablement requires manual migration")
        concurrency = worker_env.get(b"OPENAI_REAUTH_CONCURRENCY", b"3").decode()
        if not concurrency.isascii() or not concurrency.isdigit() or not 1 <= int(concurrency) <= 16:
            raise ValueError("invalid legacy concurrency")
        backup = BACKUP_DIR / (time.strftime("%Y%m%dT%H%M%S") + "-" + uuid.uuid4().hex[:8])
        backup.mkdir(parents=True, mode=0o700)
        (backup / "migration.json").write_text(json.dumps({"version": version, "digest": digest,
                "service": service, "worker_service": sidecar, "worker_enabled": enabled,
                "worker_pid": worker_pid, "legacy_fragment": property_value(sidecar, "FragmentPath"),
                "managed_root": str(root), "recovery_source": str(recovery)}, indent=2) + "\n")
        os.chmod(backup / "migration.json", 0o600)
        started = int(time.time())
        try:
            command("systemctl", "stop", sidecar)
            copy_recovery(recovery, root / "totp-recovery")
            runtime.rename(target)
            journal = root / "totp-recovery"
            owned = [root, target, *target.rglob("*")]
            if journal.exists():
                owned.extend([journal, *journal.rglob("*")])
            for path in owned:
                os.chown(path, account.pw_uid, account.pw_gid)
            dropin.parent.mkdir(parents=True, exist_ok=True)
            settings = "[Service]\nUnsetEnvironment=OPENAI_REAUTH_WORKER_TOKEN\n"
            if b"OPENAI_REAUTH_CONCURRENCY" not in app_env and b"OPENAI_REAUTH_CONCURRENCY" in worker_env:
                settings += "Environment=OPENAI_REAUTH_CONCURRENCY=" + str(int(concurrency)) + "\n"
            dropin.write_text(settings)
            command("systemctl", "disable", sidecar)
            command("systemctl", "daemon-reload")
            command("systemctl", "restart", service)
            wait_ready(service, root, started)
        except Exception:
            # Keep both journals and runtime files for recovery; restore only control.
            dropin.unlink(missing_ok=True)
            command("systemctl", "daemon-reload")
            command("systemctl", "restart", service)
            if enabled == "enabled":
                command("systemctl", "enable", sidecar)
            command("systemctl", "start", sidecar)
            raise RuntimeError("migration failed; legacy service restored; inspect retained migration files") from None
        print("Managed Worker ready: version=" + version + " sha256=" + digest[7:])
        print("Migration record: " + str(backup))
        print("Original recovery directory and legacy service configuration retained.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--drained", action="store_true", help="confirm active tasks finished and new submissions paused")
    parser.add_argument("--data-dir", required=True, help="the application's actual persistent DATA_DIR")
    parser.add_argument("--service", default="sub2api.service")
    parser.add_argument("--worker-service", default="sub2api-openai-reauth-worker.service")
    args = parser.parse_args()
    try:
        migrate(args)
    except Exception as exc:
        # Never expose raw subprocess output, environment or journal contents.
        print("Worker migration failed: " + (str(exc) if type(exc) in (ValueError, RuntimeError) else type(exc).__name__))
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
