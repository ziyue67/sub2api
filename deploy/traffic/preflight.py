#!/usr/bin/env python3
"""Validate a redacted multi-host contract offline; never apply or probe it."""

import argparse
import json
from pathlib import Path
import re
import sys
from urllib.parse import urlsplit


MAX_BYTES = 256 * 1024
IDENTIFIER = re.compile(r"[a-z0-9_-]{1,64}\Z")
BUCKET = re.compile(r"(0|[1-9][0-9]*):([a-z0-9_-]{1,64}):(single|forced|mixed)\Z")
CAPABILITIES = {"http-responses", "native-ws", "bps-relay"}


def https_origin(value):
    if (not isinstance(value, str) or not value or "\\" in value
            or any(c.isspace() or ord(c) < 32 or ord(c) == 127 for c in value)):
        return None
    try:
        u = urlsplit(value)
        port = u.port
        if (u.scheme != "https" or not u.hostname or u.username is not None
                or u.password is not None or u.path not in ("", "/")
                or "?" in value or "#" in value or "%" in u.netloc or u.netloc.endswith(":")
                or (port is not None and not 1 <= port <= 65535)):
            return None
        # Collapse explicit default ports/case when detecting duplicate owners.
        return u.hostname.lower(), port or 443
    except ValueError:
        return None


def validate(document):
    """Return stable codes and index-based paths; never echo input values."""
    errors = []

    def fail(path, code):
        errors.append({"path": path, "code": code})

    def object_fields(value, allowed, path):
        if not isinstance(value, dict):
            fail(path, "object_required")
            return False
        if set(value) - allowed:
            fail(path, "unknown_fields")
        return True

    if not object_fields(document, {"version", "nodes"}, "$"):
        return errors
    if type(document.get("version")) is not int or document["version"] != 1:
        fail("$.version", "unsupported_version")
    nodes = document.get("nodes")
    if not isinstance(nodes, list) or not 2 <= len(nodes) <= 16:
        fail("$.nodes", "requires_2_to_16_nodes")
        return errors
    identities, ws_ids, origins = set(), set(), set()
    writers = 0
    published, required = set(), set()
    for index, node in enumerate(nodes):
        path = f"$.nodes[{index}]"
        if not object_fields(node, {"id", "runtime", "capabilities", "scheduler_buckets"}, path):
            continue
        identity = node.get("id")
        if not isinstance(identity, str) or not IDENTIFIER.fullmatch(identity):
            fail(path + ".id", "safe_identifier_required")
        elif identity in identities:
            fail(path + ".id", "duplicate_node_id")
        else:
            identities.add(identity)
        caps = node.get("capabilities")
        if (not isinstance(caps, list) or not caps
                or any(not isinstance(c, str) or c not in CAPABILITIES for c in caps)):
            fail(path + ".capabilities", "known_capabilities_required")
            caps = []
        elif len(set(caps)) != len(caps):
            fail(path + ".capabilities", "duplicate_capability")
        runtime = node.get("runtime")
        rp = path + ".runtime"
        if not object_fields(runtime, {"role", "scheduler_snapshot_mode", "scheduler_readiness", "ws_node_owner", "image_relay_owner_origin"}, rp):
            continue
        role, mode = runtime.get("role", "full"), runtime.get("scheduler_snapshot_mode", "auto")
        if role not in ("full", "gateway") or mode not in ("auto", "writer", "reader"):
            fail(rp, "invalid_role_or_snapshot_mode")
        writer = role == "full" and mode in ("auto", "writer")
        writers += int(writer)
        if role == "gateway" and mode == "writer":
            fail(rp, "gateway_cannot_publish")

        def buckets(value, bp):
            if not isinstance(value, list) or not 1 <= len(value) <= 16:
                fail(bp, "requires_1_to_16_buckets")
                return set()
            valid = set()
            for entry in value:
                match = BUCKET.fullmatch(entry) if isinstance(entry, str) else None
                if (not match or int(match[1]) > 9223372036854775807
                        or (match[3] == "mixed" and match[2] not in ("anthropic", "gemini"))):
                    fail(bp, "invalid_bucket")
                elif entry in valid:
                    fail(bp, "duplicate_bucket")
                else:
                    valid.add(entry)
            return valid

        assigned = buckets(node.get("scheduler_buckets"), path + ".scheduler_buckets")
        if writer:
            published.update(assigned)
        else:
            required.update(assigned)
        readiness = runtime.get("scheduler_readiness", {})
        if object_fields(readiness, {"enabled", "buckets", "max_age_seconds"}, rp + ".scheduler_readiness"):
            enabled = readiness.get("enabled", False)
            age = readiness.get("max_age_seconds", 900)
            if type(enabled) is not bool or type(age) is not int or not 0 <= age <= 86400:
                fail(rp + ".scheduler_readiness", "invalid_readiness_settings")
            if not writer and enabled is not True:
                fail(rp + ".scheduler_readiness", "reader_gate_required")
            if enabled is True:
                if role != "gateway":
                    fail(rp + ".scheduler_readiness", "gate_requires_gateway")
                scopes = buckets(readiness.get("buckets"), rp + ".scheduler_readiness.buckets")
                if not assigned <= scopes:
                    fail(rp + ".scheduler_readiness.buckets", "assigned_scope_not_covered")
        ws = runtime.get("ws_node_owner", {})
        if object_fields(ws, {"enabled", "node_id"}, rp + ".ws_node_owner"):
            if type(ws.get("enabled", False)) is not bool:
                fail(rp + ".ws_node_owner", "boolean_required")
            if "native-ws" in caps and ws.get("enabled") is not True:
                fail(rp + ".ws_node_owner", "native_ws_gate_required")
            if ws.get("enabled") is True:
                node_id = ws.get("node_id")
                if not isinstance(node_id, str) or not IDENTIFIER.fullmatch(node_id):
                    fail(rp + ".ws_node_owner.node_id", "safe_identifier_required")
                elif node_id in ws_ids:
                    fail(rp + ".ws_node_owner.node_id", "duplicate_ws_identity")
                else:
                    ws_ids.add(node_id)
        origin = runtime.get("image_relay_owner_origin", "")
        if "bps-relay" in caps and not origin:
            fail(rp + ".image_relay_owner_origin", "relay_owner_origin_required")
        if origin != "":
            normalized = https_origin(origin)
            if normalized is None:
                fail(rp + ".image_relay_owner_origin", "https_origin_required")
            elif normalized in origins:
                fail(rp + ".image_relay_owner_origin", "duplicate_owner_origin")
            else:
                origins.add(normalized)
    if writers != 1:
        fail("$.nodes", "exactly_one_writer_required")
    if not required <= published:
        fail("$.nodes", "reader_scope_not_published")
    return errors


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate field")
        result[key] = value
    return result


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("manifest", help="redacted v1 JSON, never an application config or secret file")
    args = parser.parse_args(argv)
    try:
        with Path(args.manifest).open("rb") as source:
            raw = source.read(MAX_BYTES + 1)
        if len(raw) > MAX_BYTES:
            raise ValueError("manifest too large")
        document = json.loads(raw, object_pairs_hook=unique_object)
        errors = validate(document)
    except (OSError, ValueError, UnicodeError, RecursionError):
        # Never print the source path, parser text or accidental input secrets.
        errors = [{"path": "$", "code": "manifest_unreadable_or_invalid"}]
    print(json.dumps({"schema_version": 1, "static_only": True, "ok": not errors, "errors": errors,
                      "unverified": ["runtime_matches_manifest", "dns_tls_owner_reachability", "shared_state_identity", "account_eligibility", "live_ws_connection", "billing_idempotency", "transparent_recovery"]}))
    return int(bool(errors))


if __name__ == "__main__":
    sys.exit(main())
