#!/usr/bin/env python3
"""Close API/layout dependencies of pinned selective historical RED overlays.

The selected regression fixtures and behavioral validators stay byte-identical.
API dependencies are supplied through explicit arguments, unrelated layout
fields and candidate helpers. The handler's three HTTP body-read call sites
are adapted separately; selected WebSocket methods remain byte-identical.
Every adaptation reverses to the exact historical blob and is recorded.
"""
import argparse
import hashlib
import json
import subprocess
from pathlib import Path

BRIDGE = "backend/internal/service/openai_ws_http_bridge.go"
HANDLER = "backend/internal/handler/openai_gateway_handler.go"
IMAGES = "backend/internal/service/openai_images_responses.go"
IMAGES_BASELINE = "0f5313e50705ef99dad2691edd1147d9f803a96c"
IMAGES_METHOD = b"func (s *OpenAIGatewayService) tryWriteOpenAIImagesStreamLimitError("
BRIDGE_BASELINES = {
    "2927209f5492d8cb690b83a1964b2f093c19ad7a",
    "b9feadf7dd6228a4117e360821d9994b7cc54897",
    "04ec5e737ecb925faa2e8f3b00efff816715c55c",
    "72c21bedea567d8032f736adba16edcf5645ca19",
}
HANDLER_BASELINE = "64872b526a176f5036b5ca1aac61a59c97c1c58e"
OLD_CALL = b"newResponsesClientToolStreamBody(resp.Body, clientToolMapping, maxLineSize)"
NEW_CALL = b"newResponsesClientToolStreamBody(resp.Body, clientToolMapping, maxLineSize, s.cfg)"
ARCHIVE_FIELD = b"\timageArchive               *service.ImageArchiveService\n"
LANE_VALUE = b"\n// Compile-only value for unrelated candidate callers; historical acquisition never returns it.\nconst openAISlotAcquireLaneUnavailable openAISlotAcquireResult = 3\n"


def git(root, *args):
    return subprocess.check_output(["git", "-C", str(root), *args])


def blob(data):
    return hashlib.sha1(b"blob " + str(len(data)).encode() + b"\0" + data).hexdigest()


def definition(source, anchor):
    if source.count(anchor) != 1:
        raise ValueError("missing or ambiguous candidate declaration: " + anchor.decode())
    start = source.index(anchor)
    return source[start:source.index(b"\n}\n", start) + 3]


HTTP_IMPORT = b'\tpkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"\n'
HTTP_CALLS = (
    (b"middleware2.RejectRequestBodyReadError(c, err)",
     b'middleware2.RejectRequestBodyReadError(c, "handler.openai_gateway", err)', 2),
    (b"logRequestBodyReadFailure(reqLog, c.Request, err)",
     b"logRequestBodyReadFailure(reqLog, pkghttputil.DescribeRequestBodyReadFailure(c.Request, err))", 1),
)
HANDLER_HELPERS = (
    b"func shouldRecordCyberPolicyFallbackUsage(",
    b"func openAISlotAcquireResultForVeto(",
    b"func (h *OpenAIGatewayHandler) handleForwardRequestBodyMemoryOverload(",
)


def handler_suffix(candidate):
    return LANE_VALUE + b"".join(b"\n" + definition(candidate, anchor) for anchor in HANDLER_HELPERS)


def adapt(target, baseline, historical, candidate):
    if target == IMAGES:
        if baseline != IMAGES_BASELINE or IMAGES_METHOD in historical or candidate.count(IMAGES_METHOD) != 1:
            raise ValueError("unexpected historical image-stream contract")
        start = candidate.index(IMAGES_METHOD)
        end = candidate.index(b"\n}\n", start) + 3
        fragment = b"\n" + candidate[start:end]
        result = historical + fragment
        if result.removesuffix(fragment) != historical:
            raise ValueError("image-stream compatibility changed historical methods")
        return result, ["append unchanged candidate image-stream limit error helper for unrelated image callers"]
    if target == BRIDGE:
        if baseline not in BRIDGE_BASELINES:
            raise ValueError("unreviewed bridge baseline")
        if historical.count(OLD_CALL) != 1 or NEW_CALL in historical:
            raise ValueError("ambiguous historical three-argument bridge caller")
        # The candidate factory now requires the same gateway config already
        # used immediately above the call to choose maxLineSize. No recovery,
        # ownership, history, delivery, or usage statement is changed.
        current_signature = b"maxLineSize int,\n\tcfg *config.Config,"
        if current_signature not in candidate:
            raise ValueError("candidate factory signature changed")
        result = historical.replace(OLD_CALL, NEW_CALL, 1)
        if result.replace(NEW_CALL, OLD_CALL, 1) != historical:
            raise ValueError("bridge compatibility failed exact source roundtrip")
        return result, ["supply existing s.cfg to the client-tool stream factory"]
    if target != HANDLER or baseline != HANDLER_BASELINE:
        raise ValueError("unreviewed handler baseline")
    anchor = b"type OpenAIGatewayHandler struct {\n"
    if historical.count(anchor) != 1 or b"imageArchive" in historical or b"openAISlotAcquireLaneUnavailable" in historical:
        raise ValueError("unexpected historical handler layout")
    if candidate.count(ARCHIVE_FIELD) != 1 or b"\topenAISlotAcquireLaneUnavailable\n" not in candidate:
        raise ValueError("candidate archive/lane contract changed")
    if HTTP_IMPORT in historical or candidate.count(HTTP_IMPORT) != 1:
        raise ValueError("unexpected historical HTTP utility import")
    if any(anchor in historical for anchor in HANDLER_HELPERS):
        raise ValueError("historical handler already defines candidate-only helpers")
    result = historical.replace(anchor, anchor + ARCHIVE_FIELD, 1)
    result = result.replace(b"import (\n", b"import (\n" + HTTP_IMPORT, 1)
    changes = ["archive pointer layout for unrelated image-history callers",
               "unreturned lane enum value for unrelated media callers"]
    for old, new, count in HTTP_CALLS:
        if historical.count(old) != count or new in historical:
            raise ValueError("unexpected historical HTTP body-read caller")
        result = result.replace(old, new)
        changes.append("HTTP body-read API adapter outside selected WebSocket methods: " + new.decode())
    suffix = handler_suffix(candidate)
    result += suffix
    changes.extend("append unchanged candidate helper for unrelated callers: " + declaration.decode()
                   for declaration in HANDLER_HELPERS)
    restored = result.removesuffix(suffix).replace(ARCHIVE_FIELD, b"", 1).replace(HTTP_IMPORT, b"", 1)
    for old, new, count in HTTP_CALLS:
        restored = restored.replace(new, old, count)
    if restored != historical:
        raise ValueError("handler compatibility failed exact historical source roundtrip")
    # The selected ingress entry point and reporting predicates must remain the
    # literal old implementation, independently of the reversible HTTP adapters.
    for selected in (b"func (h *OpenAIGatewayHandler) ResponsesWebSocket(",
                     b"func shouldReportOpenAIWSProxyAccountFailure(",
                     b"func openAIWSIngressEndedByClient("):
        if definition(result, selected) != definition(historical, selected):
            raise ValueError("selected historical WebSocket method changed")
    return result, changes


def prepare(baseline, tree, candidate, target, fixtures):
    root = Path(tree).resolve()
    baseline = git(root, "rev-parse", baseline + "^{commit}").decode().strip()
    candidate = git(root, "rev-parse", candidate + "^{commit}").decode().strip()
    if git(root, "rev-parse", "HEAD").decode().strip() != candidate:
        raise ValueError("requires a disposable candidate checkout")
    fixture_blobs = {}
    for fixture in fixtures:
        expected = git(root, "show", candidate + ":" + fixture)
        if (root / fixture).read_bytes() != expected:
            raise ValueError("selected candidate oracle changed: " + fixture)
        fixture_blobs[fixture] = blob(expected)
    historical = git(root, "show", baseline + ":" + target)
    if (root / target).read_bytes() != historical:
        raise ValueError("historical overlay is dirty or already adapted: " + target)
    contract = "backend/internal/service/openai_gateway_grok_tool_protocol.go" if target == BRIDGE else target
    expected_contract = git(root, "show", candidate + ":" + contract)
    if target == BRIDGE and (root / contract).read_bytes() != expected_contract:
        raise ValueError("candidate factory implementation changed")
    result, changes = adapt(target, baseline, historical, expected_contract)
    unrelated = {}
    if target == HANDLER:
        # Go compiles every package fixture even when only attribution tests
        # run. This independent queue fixture calls the old admission arity.
        path = "backend/internal/handler/openai_ws_key_queue_test.go"
        if path in fixtures:
            raise ValueError("cannot replace a selected candidate oracle")
        expected_fixture = git(root, "show", candidate + ":" + path)
        if (root / path).read_bytes() != expected_fixture:
            raise ValueError("unrelated candidate fixture is dirty: " + path)
        prior_fixture = git(root, "show", baseline + ":" + path)
        unrelated[path] = {"candidate_blob": blob(expected_fixture),
                           "historical_blob": blob(prior_fixture)}
    (root / target).write_bytes(result)
    if unrelated:
        (root / path).write_bytes(prior_fixture)
    return {"baseline": baseline, "candidate": candidate, "path": target,
            "historical_blob": blob(historical), "adapted_blob": blob(result),
            "candidate_contract_blob": blob(expected_contract),
            "adaptations": changes, "selected_candidate_oracles": fixture_blobs,
            "unrelated_fixture_overlays": unrelated}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("baseline")
    parser.add_argument("tree")
    parser.add_argument("candidate")
    parser.add_argument("--target", choices=("bridge", "handler", "images"), required=True)
    parser.add_argument("--fixture", action="append", default=[])
    parser.add_argument("--manifest", required=True)
    args = parser.parse_args()
    target = {"bridge": BRIDGE, "handler": HANDLER, "images": IMAGES}[args.target]
    result = prepare(args.baseline, args.tree, args.candidate, target, args.fixture)
    text = json.dumps(result, indent=2) + "\n"
    Path(args.manifest).write_text(text)
    print(text, end="")
