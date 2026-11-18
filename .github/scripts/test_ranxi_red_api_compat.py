"""Verify pinned API adapters preserve historical logic and selected oracles."""
import importlib.util
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("ranxi_compat", Path(__file__).with_name("prepare-ranxi-red-api-compat.py"))
COMPAT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(COMPAT)
FACTORY = "backend/internal/service/openai_gateway_grok_tool_protocol.go"
FIXTURE = "backend/internal/handler/openai_ws_unsent_delta_test.go"


class RanxiAPICompatibilityTest(unittest.TestCase):
    def test_each_bridge_baseline_changes_only_the_reviewed_argument(self):
        candidate = COMPAT.git(ROOT, "show", "HEAD:" + FACTORY)
        for baseline in sorted(COMPAT.BRIDGE_BASELINES):
            with self.subTest(baseline=baseline):
                historical = COMPAT.git(ROOT, "show", baseline + ":" + COMPAT.BRIDGE)
                adapted, changes = COMPAT.adapt(COMPAT.BRIDGE, baseline, historical, candidate)
                self.assertEqual(historical, adapted.replace(COMPAT.NEW_CALL, COMPAT.OLD_CALL, 1))
                self.assertEqual(1, adapted.count(COMPAT.NEW_CALL))
                self.assertEqual(1, len(changes))

    def test_handler_http_adapters_are_reversible_and_websocket_methods_unchanged(self):
        baseline = COMPAT.HANDLER_BASELINE
        historical = COMPAT.git(ROOT, "show", baseline + ":" + COMPAT.HANDLER)
        candidate = COMPAT.git(ROOT, "show", "HEAD:" + COMPAT.HANDLER)
        adapted, changes = COMPAT.adapt(COMPAT.HANDLER, baseline, historical, candidate)
        restored = adapted.removesuffix(COMPAT.handler_suffix(candidate))
        restored = restored.replace(COMPAT.ARCHIVE_FIELD, b"", 1).replace(COMPAT.HTTP_IMPORT, b"", 1)
        for old, new, count in COMPAT.HTTP_CALLS:
            self.assertEqual(count, adapted.count(new))
            restored = restored.replace(new, old, count)
        self.assertEqual(historical, restored)
        for anchor in (b"func (h *OpenAIGatewayHandler) ResponsesWebSocket(",
                       b"func shouldReportOpenAIWSProxyAccountFailure(",
                       b"func openAIWSIngressEndedByClient("):
            self.assertEqual(COMPAT.definition(historical, anchor), COMPAT.definition(adapted, anchor))
        for anchor in COMPAT.HANDLER_HELPERS:
            self.assertNotIn(anchor, historical)
            self.assertEqual(COMPAT.definition(candidate, anchor), COMPAT.definition(adapted, anchor))
        self.assertTrue(any("HTTP body-read" in change for change in changes))
        with self.assertRaises(ValueError):
            COMPAT.adapt(COMPAT.HANDLER, baseline, historical, candidate.replace(COMPAT.HTTP_IMPORT, b""))
        old, new, _ = COMPAT.HTTP_CALLS[0]
        with self.assertRaises(ValueError):
            COMPAT.adapt(COMPAT.HANDLER, baseline, historical.replace(old, new, 1), candidate)

    def test_images_append_only_the_unchanged_candidate_limit_helper(self):
        historical = COMPAT.git(ROOT, "show", COMPAT.IMAGES_BASELINE + ":" + COMPAT.IMAGES)
        candidate = COMPAT.git(ROOT, "show", "HEAD:" + COMPAT.IMAGES)
        adapted, changes = COMPAT.adapt(COMPAT.IMAGES, COMPAT.IMAGES_BASELINE, historical, candidate)
        self.assertTrue(adapted.startswith(historical))
        fragment = adapted[len(historical):].strip()
        self.assertIn(fragment, candidate)
        self.assertEqual(1, fragment.count(COMPAT.IMAGES_METHOD))
        self.assertEqual(1, len(changes))
        with self.assertRaises(ValueError):
            COMPAT.adapt(COMPAT.IMAGES, "unreviewed", historical, candidate)

    def test_unknown_baseline_or_changed_contract_is_rejected(self):
        baseline = sorted(COMPAT.BRIDGE_BASELINES)[0]
        historical = COMPAT.git(ROOT, "show", baseline + ":" + COMPAT.BRIDGE)
        candidate = COMPAT.git(ROOT, "show", "HEAD:" + FACTORY)
        with self.assertRaises(ValueError):
            COMPAT.adapt(COMPAT.BRIDGE, "unreviewed", historical, candidate)
        with self.assertRaises(ValueError):
            COMPAT.adapt(COMPAT.BRIDGE, baseline, historical, b"different signature")
        with self.assertRaises(ValueError):
            COMPAT.adapt(COMPAT.BRIDGE, baseline, historical.replace(COMPAT.OLD_CALL, COMPAT.NEW_CALL), candidate)

    def make_checkout(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        tree = Path(temp.name) / "checkout"
        subprocess.run(["git", "clone", "--shared", "--no-checkout", str(ROOT), str(tree)], check=True, capture_output=True)
        candidate = COMPAT.git(tree, "rev-parse", "HEAD").decode().strip()
        baseline = sorted(COMPAT.BRIDGE_BASELINES)[0]
        for path, revision in [(COMPAT.BRIDGE, baseline), (FACTORY, candidate), (FIXTURE, candidate)]:
            target = tree / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(COMPAT.git(tree, "show", revision + ":" + path))
        return tree, baseline, candidate

    def test_selected_fixture_is_unchanged_and_recorded(self):
        tree, baseline, candidate = self.make_checkout()
        fixture = (tree / FIXTURE).read_bytes()
        result = COMPAT.prepare(baseline, tree, candidate, COMPAT.BRIDGE, [FIXTURE])
        self.assertEqual(fixture, (tree / FIXTURE).read_bytes())
        self.assertEqual(COMPAT.blob(fixture), result["selected_candidate_oracles"][FIXTURE])
        self.assertEqual(COMPAT.blob((tree / COMPAT.BRIDGE).read_bytes()), result["adapted_blob"])

    def test_handler_records_exact_independent_queue_fixture_and_rejects_dirty_copy(self):
        queue = "backend/internal/handler/openai_ws_key_queue_test.go"
        for dirty in (False, True):
            with self.subTest(dirty=dirty):
                tree, _, candidate = self.make_checkout()
                baseline = COMPAT.HANDLER_BASELINE
                for path, revision in ((COMPAT.HANDLER, baseline), (queue, candidate)):
                    target = tree / path
                    target.parent.mkdir(parents=True, exist_ok=True)
                    target.write_bytes(COMPAT.git(tree, "show", revision + ":" + path))
                before = (tree / COMPAT.HANDLER).read_bytes()
                if dirty:
                    (tree / queue).write_bytes((tree / queue).read_bytes() + b"\n// dirty\n")
                    with self.assertRaises(ValueError):
                        COMPAT.prepare(baseline, tree, candidate, COMPAT.HANDLER, [FIXTURE])
                    self.assertEqual(before, (tree / COMPAT.HANDLER).read_bytes())
                else:
                    fixture_before = (tree / FIXTURE).read_bytes()
                    result = COMPAT.prepare(baseline, tree, candidate, COMPAT.HANDLER, [FIXTURE])
                    prior = COMPAT.git(tree, "show", baseline + ":" + queue)
                    self.assertEqual(prior, (tree / queue).read_bytes())
                    self.assertEqual(COMPAT.blob(prior), result["unrelated_fixture_overlays"][queue]["historical_blob"])
                    self.assertEqual(fixture_before, (tree / FIXTURE).read_bytes())

    def test_dirty_source_factory_or_fixture_fails_before_writing(self):
        for path in [COMPAT.BRIDGE, FACTORY, FIXTURE]:
            with self.subTest(path=path):
                tree, baseline, candidate = self.make_checkout()
                target = tree / path
                target.write_bytes(target.read_bytes() + b"\n// dirty fixture\n")
                before = (tree / COMPAT.BRIDGE).read_bytes()
                with self.assertRaises(ValueError):
                    COMPAT.prepare(baseline, tree, candidate, COMPAT.BRIDGE, [FIXTURE])
                self.assertEqual(before, (tree / COMPAT.BRIDGE).read_bytes())


if __name__ == "__main__":
    unittest.main()
