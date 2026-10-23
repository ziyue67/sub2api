"""Render the real gateway template offline with CI's checksum-pinned Kustomize."""

from pathlib import Path
import subprocess
import sys
import unittest

import yaml


RENDER = Path(__file__).parents[1] / "kubernetes" / "render.py"
IMAGE = "ghcr.io/ranxi2001/sub2api@sha256:" + "a" * 64


class RealGatewayRenderTests(unittest.TestCase):
    def render(self, namespace, replicas=None):
        command = [sys.executable, str(RENDER), "--namespace", namespace, "--image", IMAGE]
        if replicas is not None:
            command.extend(["--replicas", str(replicas)])
        result = subprocess.run(command, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        documents = list(yaml.safe_load_all(result.stdout))
        self.assertTrue(documents)
        self.assertNotIn("build-with-k8s-runtime-required", result.stdout)
        namespace_objects = [obj for obj in documents if obj["kind"] == "Namespace"]
        self.assertEqual(len(namespace_objects), 1)
        self.assertEqual(namespace_objects[0]["metadata"]["name"], namespace)
        for obj in documents:
            if obj["kind"] != "Namespace":
                self.assertEqual(obj["metadata"]["namespace"], namespace)
        workload = next(obj for obj in documents if obj["kind"] == "StatefulSet")
        self.assertEqual(workload["spec"]["replicas"], replicas if replicas is not None else 0)
        pod = workload["spec"]["template"]["spec"]
        self.assertEqual(len(pod["initContainers"]), 1)
        self.assertEqual(len(pod["containers"]), 1)
        for container in pod["initContainers"] + pod["containers"]:
            self.assertEqual(container["image"], IMAGE)
        return documents, workload

    def test_default_namespace_stages_without_starting_gateway(self):
        self.render("sub2api")

    def test_custom_namespace_preserves_fixed_instance_service_and_private_pvc(self):
        documents, workload = self.render("gateway-canary", replicas=2)
        fixed_service = next(obj for obj in documents
                             if obj["kind"] == "Service" and obj["metadata"]["name"] == "sub2api-gateway-0")
        self.assertEqual(fixed_service["spec"]["selector"]["statefulset.kubernetes.io/pod-name"],
                         "sub2api-gateway-0")
        claim = workload["spec"]["volumeClaimTemplates"][0]
        self.assertEqual(claim["metadata"]["name"], "data")
        self.assertEqual(claim["spec"]["accessModes"], ["ReadWriteOnce"])
        pod = workload["spec"]["template"]["spec"]
        self.assertFalse(any("persistentVolumeClaim" in volume for volume in pod["volumes"]))


if __name__ == "__main__":
    unittest.main()
