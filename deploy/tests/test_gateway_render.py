"""Offline renderer boundary tests. CI only; no cluster or container required."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


RENDER = Path(__file__).parents[1] / "kubernetes" / "render.py"
IMAGE = "ghcr.io/ranxi2001/sub2api@sha256:" + "a" * 64
FAKE_TOOL = '''#!{python}
import json, os
from pathlib import Path
import sys
command, directory = sys.argv[1:]
assert command in ("build", "kustomize"), "unexpected cluster operation"
overlay = json.loads(Path(directory, "kustomization.yaml").read_text())
Path(os.environ["CAPTURE"]).write_text(json.dumps({{"command": command, "overlay": overlay}}))
print(os.environ.get("RENDER_OUTPUT", "apiVersion: v1\\nkind: List\\nitems: []"))
if os.environ.get("RENDER_FAIL"):
    print("simulated command failure", file=sys.stderr)
    sys.exit(9)
'''


class GatewayRenderTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.capture = self.directory / "capture.json"
        self.env = dict(os.environ, PATH=str(self.directory), CAPTURE=str(self.capture))
        self.install_tool("kustomize")

    def install_tool(self, name):
        tool = self.directory / name
        tool.write_text(FAKE_TOOL.format(python=sys.executable))
        tool.chmod(0o755)

    def invoke(self, *arguments, extra_env=None):
        return subprocess.run(
            [sys.executable, str(RENDER), *arguments],
            env=dict(self.env, **(extra_env or {})),
            capture_output=True, text=True,
        )

    def test_custom_namespace_pins_both_workload_and_namespace_resource(self):
        result = self.invoke("--namespace", "gateway-canary", "--image", IMAGE, "--replicas", "2")
        self.assertEqual(result.returncode, 0, result.stderr)
        record = json.loads(self.capture.read_text())
        self.assertEqual(record["command"], "build")
        overlay = record["overlay"]
        self.assertEqual(overlay["resources"], ["base"])
        self.assertEqual(overlay["namespace"], "gateway-canary")
        namespace_patch = next(p for p in overlay["patches"] if p["target"]["kind"] == "Namespace")
        self.assertIn({"op": "replace", "path": "/metadata/name", "value": "gateway-canary"},
                      json.loads(namespace_patch["patch"]))
        self.assertEqual(overlay["replicas"], [{"name": "sub2api-gateway", "count": 2}])
        self.assertEqual(overlay["images"], [{
            "name": "ghcr.io/ranxi2001/sub2api", "newName": "ghcr.io/ranxi2001/sub2api",
            "digest": "sha256:" + "a" * 64,
        }])
        self.assertIn("Offline render only", result.stdout)

    def test_default_is_staged_zero_replicas_and_kubectl_is_offline(self):
        (self.directory / "kustomize").unlink()
        self.install_tool("kubectl")
        result = self.invoke("--namespace", "sub2api", "--image", IMAGE)
        self.assertEqual(result.returncode, 0, result.stderr)
        record = json.loads(self.capture.read_text())
        self.assertEqual(record["command"], "kustomize")
        self.assertEqual(record["overlay"]["replicas"][0]["count"], 0)

    def test_invalid_inputs_do_not_invoke_tool_or_emit_manifest(self):
        for namespace, image, replicas in [
            ("Bad.Namespace", IMAGE, "1"), ("a" * 64, IMAGE, "1"),
            ("sub2api", "ghcr.io/ranxi2001/sub2api:latest", "1"),
            ("sub2api", "ghcr.io/other/sub2api@sha256:" + "a" * 64, "1"),
            ("sub2api", IMAGE, "-1"), ("sub2api", IMAGE, "33"),
        ]:
            with self.subTest(namespace=namespace, image=image, replicas=replicas):
                result = self.invoke("--namespace", namespace, "--image", image, "--replicas", replicas)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, "")
                self.assertFalse(self.capture.exists())

    def test_failed_command_does_not_publish_partial_manifest(self):
        result = self.invoke("--namespace", "sub2api", "--image", IMAGE,
                             extra_env={"RENDER_FAIL": "1"})
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")
        self.assertIn("simulated command failure", result.stderr)

    def test_placeholder_image_is_rejected_without_output(self):
        result = self.invoke("--namespace", "sub2api", "--image", IMAGE, extra_env={
            "RENDER_OUTPUT": "image: ghcr.io/ranxi2001/sub2api:build-with-k8s-runtime-required",
        })
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")
        self.assertIn("unpinned template image", result.stderr)

    def test_missing_renderer_is_reported_without_output(self):
        (self.directory / "kustomize").unlink()
        result = self.invoke("--namespace", "sub2api", "--image", IMAGE)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")
        self.assertIn("install kustomize or kubectl", result.stderr)


if __name__ == "__main__":
    unittest.main()
