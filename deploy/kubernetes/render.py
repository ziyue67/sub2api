#!/usr/bin/env python3
"""Render a digest-pinned gateway manifest offline; never contact a cluster."""

import argparse
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--image", required=True, help="ghcr.io/ranxi2001/sub2api@sha256:<64 lowercase hex>")
    parser.add_argument("--replicas", type=int, choices=range(0, 33), default=0)
    args = parser.parse_args()
    if not re.fullmatch(r"[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?", args.namespace):
        parser.error("namespace must be a DNS label of at most 63 characters")
    image_match = re.fullmatch(r"(ghcr\.io/ranxi2001/sub2api)@(sha256:[0-9a-f]{64})", args.image)
    if not image_match:
        parser.error("image must be an owner-fork image pinned to a sha256 digest")
    standalone = shutil.which("kustomize")
    kubectl = shutil.which("kubectl")
    if not standalone and not kubectl:
        parser.error("install kustomize or kubectl for offline rendering")
    base = Path(__file__).resolve().parent
    overlay = {
        "apiVersion": "kustomize.config.k8s.io/v1beta1",
        "kind": "Kustomization",
        "namespace": args.namespace,
        "resources": ["base"],
        # Namespace's metadata.name is cluster-scoped; set it explicitly rather
        # than relying on a particular Kustomize namespace-transformer version.
        "patches": [{
            "target": {"version": "v1", "kind": "Namespace", "name": "sub2api"},
            "patch": json.dumps([{
                "op": "replace", "path": "/metadata/name", "value": args.namespace,
            }]),
        }],
        "images": [{"name": image_match[1], "newName": image_match[1], "digest": image_match[2]}],
        "replicas": [{"name": "sub2api-gateway", "count": args.replicas}],
    }
    with tempfile.TemporaryDirectory(prefix="sub2api-gateway-render-") as temp_dir:
        # Keep all resources under the build root. Absolute resource directories
        # are rejected by Kustomize's default loader; never relax load security.
        staged_base = Path(temp_dir, "base")
        staged_base.mkdir()
        for name in ("kustomization.yaml", "namespace.yaml", "gateway.yaml",
                     "services.yaml", "disruption-budget.yaml"):
            shutil.copyfile(base / name, staged_base / name)
        Path(temp_dir, "kustomization.yaml").write_text(json.dumps(overlay), encoding="utf-8")
        command = [standalone, "build", temp_dir] if standalone else [kubectl, "kustomize", temp_dir]
        result = subprocess.run(command, check=True, capture_output=True, text=True)
    if "build-with-k8s-runtime-required" in result.stdout:
        raise RuntimeError("an unpinned template image remains; refusing output")
    print("# Offline render only. No cluster, credentials, DNS or traffic were changed.")
    print(f"# namespace={args.namespace} replicas={args.replicas} image={args.image}")
    print(result.stdout, end="")


if __name__ == "__main__":
    try:
        main()
    except (OSError, subprocess.CalledProcessError, RuntimeError) as exc:
        print(f"render failed: {exc}", file=sys.stderr)
        if isinstance(exc, subprocess.CalledProcessError) and exc.stderr:
            print(exc.stderr, file=sys.stderr, end="")
        sys.exit(1)
