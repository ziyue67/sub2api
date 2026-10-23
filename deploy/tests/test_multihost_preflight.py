"""CI-only offline contract regressions; never contact real nodes or services."""

from copy import deepcopy
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


ROOT = Path(__file__).parents[1] / "traffic"
SCRIPT = ROOT / "preflight.py"
spec = importlib.util.spec_from_file_location("multihost_preflight", SCRIPT)
preflight = importlib.util.module_from_spec(spec)
spec.loader.exec_module(preflight)


class MultiHostPreflightTests(unittest.TestCase):
    def setUp(self):
        self.document = json.loads((ROOT / "multi-host.example.json").read_text())

    def codes(self, document):
        return {entry["code"] for entry in preflight.validate(document)}

    def invoke(self, raw):
        with tempfile.TemporaryDirectory() as directory:
            manifest = Path(directory) / "redacted.json"
            manifest.write_bytes(raw)
            return subprocess.run([sys.executable, str(SCRIPT), str(manifest)],
                                  capture_output=True, text=True, timeout=5)

    def test_example_and_http_only_contract_need_no_network(self):
        with patch("socket.getaddrinfo", side_effect=AssertionError("no DNS allowed")):
            self.assertEqual(preflight.validate(self.document), [])
            http_only = deepcopy(self.document)
            for node in http_only["nodes"]:
                node["capabilities"] = ["http-responses"]
                node["runtime"].pop("ws_node_owner")
                node["runtime"].pop("image_relay_owner_origin")
            self.assertEqual(preflight.validate(http_only), [])

    def test_owner_origins_match_application_shared_contract(self):
        cases = Path(__file__).parents[2] / "backend/internal/util/urlvalidator/testdata/https-origin-contract.json"
        for case in json.loads(cases.read_text()):
            with self.subTest(name=case["name"]):
                self.assertEqual(preflight.https_origin(case["url"]) is not None, case["valid"])

    def test_writer_and_bucket_contracts(self):
        changes = [
            (lambda d: d["nodes"][1]["runtime"].update(role="full", scheduler_snapshot_mode="writer"), "exactly_one_writer_required"),
            (lambda d: d["nodes"][0]["runtime"].update(scheduler_snapshot_mode="reader"), "exactly_one_writer_required"),
            (lambda d: d["nodes"][1]["runtime"].update(scheduler_snapshot_mode="writer"), "gateway_cannot_publish"),
            (lambda d: d["nodes"][1]["runtime"]["scheduler_readiness"].update(enabled=False), "reader_gate_required"),
            (lambda d: d["nodes"][1]["runtime"]["scheduler_readiness"].update(buckets=["18:openai:single"]), "assigned_scope_not_covered"),
            (lambda d: d["nodes"][0].update(scheduler_buckets=["18:openai:single"]), "reader_scope_not_published"),
            (lambda d: d["nodes"][1]["runtime"]["scheduler_readiness"].update(max_age_seconds=True), "invalid_readiness_settings"),
            (lambda d: d["nodes"][1].update(scheduler_buckets=["17:openai:mixed"]), "invalid_bucket"),
        ]
        for mutate, code in changes:
            with self.subTest(code=code):
                document = deepcopy(self.document)
                mutate(document)
                self.assertIn(code, self.codes(document))

    def test_duplicate_identities_and_origin_aliases(self):
        self.document["nodes"][1]["id"] = "node-a"
        runtime = self.document["nodes"][1]["runtime"]
        runtime["ws_node_owner"]["node_id"] = "node-a"
        runtime["image_relay_owner_origin"] = "https://NODE-A.example.invalid:443/"
        self.assertTrue({"duplicate_node_id", "duplicate_ws_identity", "duplicate_owner_origin"} <= self.codes(self.document))

    def test_native_ws_and_relay_require_explicit_opt_in(self):
        runtime = self.document["nodes"][1]["runtime"]
        runtime["ws_node_owner"]["enabled"] = False
        runtime["image_relay_owner_origin"] = ""
        self.assertTrue({"native_ws_gate_required", "relay_owner_origin_required"} <= self.codes(self.document))
        for origin in ("http://node.example.invalid", "https://private:credential@node.example.invalid", "https://node.example.invalid/path", "https://node.example.invalid?private", "https://node.example.invalid/#private", "https://node.example.invalid:", "https://node.example.invalid\\path"):
            runtime["image_relay_owner_origin"] = origin
            self.assertIn("https_origin_required", self.codes(self.document))

    def test_strict_schema_types_and_credential_diagnostics(self):
        self.document["nodes"][0]["runtime"]["private-secret-field"] = "fixture-private-secret-value"
        result = self.invoke(json.dumps(self.document).encode())
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stderr, "")
        self.assertNotIn("private-secret", result.stdout)
        self.assertIn("unknown_fields", result.stdout)
        for invalid in (None, [], {"version": True, "nodes": []}, {"version": 1, "nodes": [None, {}]}):
            self.assertTrue(preflight.validate(invalid))

    def test_cli_success_is_explicitly_static(self):
        result = self.invoke(json.dumps(self.document).encode())
        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads(result.stdout)
        self.assertTrue(report["static_only"])
        self.assertTrue(report["ok"])
        self.assertIn("runtime_matches_manifest", report["unverified"])
        self.assertNotIn("example.invalid", result.stdout)

    def test_malformed_duplicate_and_oversize_inputs_do_not_echo(self):
        for raw in (b'{"version":1,"version":2,"private":"fixture"}', b"not-json-private", b"x" * (preflight.MAX_BYTES + 1)):
            result = self.invoke(raw)
            self.assertEqual(result.returncode, 1)
            self.assertNotIn("private", result.stdout + result.stderr)
            self.assertEqual(json.loads(result.stdout)["errors"], [{"path": "$", "code": "manifest_unreadable_or_invalid"}])


if __name__ == "__main__":
    unittest.main()
