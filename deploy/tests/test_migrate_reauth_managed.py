import argparse
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("migration", Path(__file__).parents[1] / "migrate-reauth-managed.py")
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)


def archive(entries):
    data = io.BytesIO()
    with tarfile.open(fileobj=data, mode="w:gz") as output:
        for name, body, kind in entries:
            item = tarfile.TarInfo(name)
            item.type = kind
            item.size = len(body) if kind == tarfile.REGTYPE else 0
            item.mode = 0o755
            output.addfile(item, io.BytesIO(body) if kind == tarfile.REGTYPE else None)
    return data.getvalue()


class MigrationTests(unittest.TestCase):
    def test_readiness_finds_worker_spawned_by_non_main_go_thread(self):
        with tempfile.TemporaryDirectory() as directory:
            proc = Path(directory)
            (proc / "100/task/100").mkdir(parents=True)
            (proc / "100/task/101").mkdir(parents=True)
            (proc / "100/task/100/children").write_text("")
            (proc / "100/task/101/children").write_text("200 ")
            (proc / "200").mkdir()
            (proc / "200/cmdline").write_bytes(b"/synthetic/worker/python\0/synthetic/worker/worker.py\0")
            with patch.object(migration, "PROC_DIR", proc), \
                 patch.object(migration, "property_value", side_effect=lambda service, key: "100" if key == "MainPID" else "active"), \
                 patch.object(migration, "command", return_value='{"path": "/api/v1/internal/openai-reauth/totp-claim", "status_code": 200}'), \
                 patch.object(migration.time, "sleep") as sleep:
                migration.wait_ready("synthetic.service", Path("/synthetic/worker"), 1)
                sleep.assert_not_called()

    def test_extract_rejects_escape_link_special_file_and_duplicates_before_writing(self):
        for name, kind in (("../escape", tarfile.REGTYPE), ("/escape", tarfile.REGTYPE),
                           ("link", tarfile.SYMTYPE), ("device", tarfile.CHRTYPE), ("run", tarfile.REGTYPE)):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                entries = [("run", b"safe", tarfile.REGTYPE), (name, b"bad", kind)]
                with self.assertRaises(ValueError):
                    migration.extract_runtime(archive(entries), Path(directory))
                self.assertEqual(list(Path(directory).iterdir()), [])

    def test_recovery_preserves_key_and_history_and_rejects_overwrite(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "old"
            source.mkdir()
            (source / "key.bin").write_bytes(b"synthetic-journal-key")
            (source / "record.enc").write_bytes(b"synthetic-ciphertext")
            destination = Path(directory) / "new"
            migration.copy_recovery(source, destination)
            self.assertEqual((destination / "key.bin").read_bytes(), (source / "key.bin").read_bytes())
            self.assertEqual((destination / "record.enc").read_bytes(), (source / "record.enc").read_bytes())
            self.assertEqual(destination.stat().st_mode & 0o777, 0o700)
            self.assertEqual((destination / "key.bin").stat().st_mode & 0o777, 0o600)
            with self.assertRaises(ValueError):
                migration.copy_recovery(source, destination)
            (source / "link").symlink_to(source / "key.bin")
            with self.assertRaises(ValueError):
                migration.copy_recovery(source, Path(directory) / "other")

    def migration_fixture(self, directory, fail_ready=False, tampered=False, protocol=False, changed=False):
        base = Path(directory)
        data = base / "data"
        data.mkdir()
        source = base / "old-journal"
        source.mkdir()
        (source / "key.bin").write_bytes(b"synthetic-key")
        (source / "history.enc").write_bytes(b"synthetic-ciphertext")
        content = archive([(name, b"synthetic", tarfile.REGTYPE) for name in
                          ("python", "node", "worker.py", "portable-verified", "ubuntu-verified")])
        digest = "sha256:" + ("0" * 64 if tampered else hashlib.sha256(content).hexdigest())
        architecture = {"x86_64": "amd64", "aarch64": "arm64"}.get(os.uname().machine)
        if not architecture:
            self.skipTest("Linux architecture fixture")
        metadata = json.dumps({"assets": [{"name": "sub2api-reauth_2.11.1_linux_" + architecture + ".tar.gz", "digest": digest}]}).encode()
        worker_env = {b"OPENAI_TOTP_JOURNAL_DIR": os.fsencode(source), b"OPENAI_REAUTH_CONCURRENCY": b"4"}
        if protocol:
            worker_env[b"TURB_ROOT"] = b"/synthetic/custom"
        calls = []
        pid_reads = 0

        def property_value(service, name):
            nonlocal pid_reads
            if name == "MainPID":
                if service == "sub2api.service":
                    pid_reads += 1
                    return "999" if changed and pid_reads > 1 else "111"
                return "222"
            return {"ActiveState": "active", "User": "root", "UnitFileState": "enabled",
                    "ExecStart": "python /opt/sub2api-reauth/current/openai_oauth_reauth_worker.py",
                    "FragmentPath": "/synthetic/unit.service"}[name]

        def command(*args):
            calls.append(args)
            if args[-1] == "--version":
                return "Sub2API 2.11.1 (commit: synthetic)"
            return ""

        patches = [patch.object(migration, "SYSTEMD_DIR", base / "units"),
                   patch.object(migration, "BACKUP_DIR", base / "backups"),
                   patch.object(migration, "property_value", side_effect=property_value),
                   patch.object(migration, "process_environment", side_effect=lambda pid: worker_env if pid == "222" else {}),
                   patch.object(migration, "application_data_dir", return_value=data),
                   patch.object(migration, "download", side_effect=lambda url, limit: metadata if "api.github.com" in url else content),
                   patch.object(migration, "command", side_effect=command),
                   patch.object(migration.subprocess, "run"), patch.object(migration.os, "chown"),
                   patch.object(migration, "wait_ready", side_effect=RuntimeError("synthetic readiness failure") if fail_ready else None)]
        for item in patches:
            item.start()
            self.addCleanup(item.stop)
        return argparse.Namespace(data_dir=str(data), service="sub2api.service", worker_service="legacy.service"), calls, base

    def test_success_keeps_recovery_and_switches_only_after_checks(self):
        with tempfile.TemporaryDirectory() as directory:
            args, calls, base = self.migration_fixture(directory)
            migration.migrate_locked(args)
            self.assertIn(("systemctl", "disable", "legacy.service"), calls)
            self.assertIn(("systemctl", "restart", "sub2api.service"), calls)
            self.assertNotIn(("systemctl", "start", "legacy.service"), calls)
            dropin = (base / "units/sub2api.service.d/90-managed-reauth.conf").read_text()
            self.assertIn("UnsetEnvironment=OPENAI_REAUTH_WORKER_TOKEN", dropin)
            self.assertIn("OPENAI_REAUTH_CONCURRENCY=4", dropin)
            self.assertEqual((base / "data/credential-worker/totp-recovery/key.bin").read_bytes(), b"synthetic-key")
            self.assertTrue((base / "old-journal/history.enc").is_file())
            migration.subprocess.run.assert_called_once()

    def test_failure_restores_sidecar_and_retains_both_journals(self):
        with tempfile.TemporaryDirectory() as directory:
            args, calls, base = self.migration_fixture(directory, fail_ready=True)
            with self.assertRaisesRegex(RuntimeError, "legacy service restored"):
                migration.migrate_locked(args)
            self.assertIn(("systemctl", "enable", "legacy.service"), calls)
            self.assertIn(("systemctl", "start", "legacy.service"), calls)
            self.assertEqual(calls.count(("systemctl", "restart", "sub2api.service")), 2)
            self.assertFalse((base / "units/sub2api.service.d/90-managed-reauth.conf").exists())
            self.assertTrue((base / "old-journal/key.bin").is_file())
            self.assertTrue((base / "data/credential-worker/totp-recovery/key.bin").is_file())

    def test_tampered_runtime_never_stops_services_or_executes_payload(self):
        with tempfile.TemporaryDirectory() as directory:
            args, calls, _base = self.migration_fixture(directory, tampered=True)
            with self.assertRaisesRegex(ValueError, "checksum"):
                migration.migrate_locked(args)
            self.assertFalse(any(call[0] == "systemctl" for call in calls))
            migration.subprocess.run.assert_not_called()

    def test_custom_email_protocol_stays_external(self):
        with tempfile.TemporaryDirectory() as directory:
            args, calls, _base = self.migration_fixture(directory, protocol=True)
            with self.assertRaisesRegex(ValueError, "email protocol"):
                migration.migrate_locked(args)
            self.assertFalse(any(call[0] == "systemctl" for call in calls))
            migration.download.assert_not_called()

    def test_concurrent_deployment_aborts_before_stop(self):
        with tempfile.TemporaryDirectory() as directory:
            args, calls, _base = self.migration_fixture(directory, changed=True)
            with self.assertRaisesRegex(ValueError, "deployment changed"):
                migration.migrate_locked(args)
            self.assertFalse(any(call[0] == "systemctl" for call in calls))


if __name__ == "__main__":
    unittest.main()
