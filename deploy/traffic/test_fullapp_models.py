"""CI only: authenticated Responses SSE through two UNMODIFIED cmd/server apps.

The model is synthetic and internal; SQL/account/key/dispatch/billing/concurrency
are the application's real paths. Separate fresh fixtures cover normal SSE,
drain, EOF and downstream cancellation without changing application policies.
"""

import http.client
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import threading
import unittest
from decimal import Decimal

import test_fullapp_bootstrap as bootstrap


class FullApplicationModelSSETest(bootstrap.RealApplicationBootstrapTest):
    def fixture_pricing(self):
        # Nonzero price makes an empty billing path observable. No money/model
        # provider exists outside this exclusive database/network fixture.
        return {"gpt-6.1-sol": {"input_cost_per_token": 0.000001,
                                "output_cost_per_token": 0.000002}}

    def configure_application(self, cfg):
        # Only the already internal Docker fixture can use plaintext upstreams;
        # production defaults/config are unchanged, no skip-certificate option.
        cfg["security"] = {"url_allowlist": {"enabled": False,
                                              "allow_private_hosts": True,
                                              "allow_insecure_http": True}}

    def setUp(self):
        super().setUp()
        tool = Path(os.environ["FULLAPP_FIXTURE_BIN"]).resolve(strict=True)
        destination = self.root / "bin" / "fullapp-fixture"
        shutil.copyfile(tool, destination)
        destination.chmod(0o755)
        self.model = self.run_container("model", bootstrap.RUNTIME_IMAGE, "256m", "0.5", [
            "--user", str(os.getuid()) + ":" + str(os.getgid()),
            "--cap-drop=ALL", "--security-opt=no-new-privileges", "--read-only",
            "--mount", "type=bind,src=" + str(self.root / "bin") + ",dst=/fixture/bin,readonly",
            "--env", "CI_FULLAPP_FIXTURE=1", "--env", "GOMAXPROCS=1",
            "--entrypoint", "/fixture/bin/fullapp-fixture"], ["serve"])
        self.model_endpoint = self.internal_endpoint(self.model, 9090)
        bootstrap.eventually(lambda: self.model_stats() is not None, 5, "synthetic model ready")
        cfg = json.loads((self.root / "a" / "config.yaml").read_text())
        self.key = "sk-fixture-" + secrets.token_hex(16)
        self.fixture_input = {"database": {name: cfg["database"][name]
                                           for name in ("host", "port", "user", "password", "dbname", "sslmode")},
                              "redis": {name: cfg["redis"][name] for name in ("host", "port", "db")},
                              "api_key": self.key, "origin": "http://" + self.model + ":9090"}
        ids = self.fixture_command("seed")
        for field in ("user_id", "api_key_id", "account_id"):
            self.assertGreater(ids[field], 0, "real repository fixture ID required")
            self.fixture_input[field] = ids[field]
        self.phase("legal-shared-user-group-key-account-and-snapshot-seeded")

    def fixture_command(self, operation):
        # stdin only: no key/config/DSN in Docker argv or report. The helper
        # rejects endpoints that aren't this fixture's generated PG/Redis names.
        result = bootstrap.docker("exec", "--interactive", "--env", "CI_FULLAPP_FIXTURE=1",
                                  getattr(self, "fixture_executor", self.apps[0]),
                                  "/fixture/bin/fullapp-fixture", operation,
                                  input_text=json.dumps(self.fixture_input), timeout=15, check=False)
        if result.returncode:
            stages = ("input", "create-user", "create-group", "create-key", "create-account-group",
                      "hydrate-account", "publish-snapshot", "observe-usage-billing-slots")
            stage = next((value for value in stages if "stage=" + value in result.stderr), "unknown")
            raise bootstrap.FixtureFailure("isolated seed/status failed stage=" + stage)
        try:
            value = json.loads(result.stdout)
        except (ValueError, TypeError):
            raise bootstrap.FixtureFailure("isolated seed/status produced no valid fixed result") from None
        self.assertIsInstance(value, dict)
        return value

    def model_stats(self):
        connection = http.client.HTTPConnection(*self.model_endpoint, timeout=2)
        try:
            connection.request("GET", "/fixture/stats")
            response = connection.getresponse()
            if response.status != 200:
                return None
            result = json.loads(response.read(4096))
            return {name: result[name] for name in ("accepted", "completed", "active")}
        except (OSError, http.client.HTTPException, ValueError, KeyError):
            return None
        finally:
            connection.close()

    def test_two_real_applications_complete_sse_once_and_settle_shared_state(self):
        expected = {app + ":8080" for app in self.apps}
        seen = set()
        # Exactly four generated requests, never retry a POST to warm the edge.
        for number in range(1, 5):
            connection = http.client.HTTPConnection(*self.edge_port, timeout=8)
            self.addCleanup(connection.close)
            body = {"model": "gpt-6.1-sol", "input": "synthetic fixture", "stream": True}
            connection.request("POST", "/v1/responses", body=json.dumps(body), headers={
                "Host": "api.fixture.invalid", "Content-Type": "application/json",
                "Authorization": "Bearer " + self.key,
                "X-Client-Request-ID": "model-fixture-" + str(number)})
            response = connection.getresponse()
            self.addCleanup(response.close)
            self.assertEqual(response.status, 200, "real authenticated gateway must return SSE")
            self.assertIn("text/event-stream", response.getheader("Content-Type", ""))
            target = response.getheader("X-Fixture-Target")
            self.assertIn(target, expected, "real Caddy dial target must be one full application")
            seen.add(target)
            payload = response.read(65536)
            self.assertIn(b'"response.completed"', payload)
            self.assertIn(b"OK", payload)
            response.close()
            connection.close()

            def settled():
                stats = self.model_stats()
                if stats != {"accepted": number, "completed": number, "active": 0}:
                    return False
                status = self.fixture_command("status")
                # Exact row counts and nonzero balance debit prevent a mere
                # HTTP200 or bypassed billing from passing this prerequisite.
                return (status["usage_rows"] == number and status["dedup_rows"] == number
                        and status["input_tokens"] == 7 * number and status["output_tokens"] == 2 * number
                        and status["balance"] < 10 and status["balance"] > 9
                        and status["user_slots"] == status["account_slots"] == status["api_key_slots"] == 0)

            bootstrap.eventually(settled, 12, "exact model generation/usage/dedup/debit/slots settled")
        self.assertEqual(seen, expected, "both full applications must process authenticated model requests")
        self.report["model_verification"] = {"generated": 4, "completed": 4,
                                              "usage_rows": 4, "dedup_rows": 4,
                                              "user_account_key_slots": 0,
                                              "scope": "synthetic internal model only; normal SSE prerequisite"}
        self.phase("both-full-app-model-sse-and-unique-settlement-verified")


class FullApplicationInflightDrainTest(FullApplicationModelSSETest):
    def model_post(self, marker, hold=False):
        connection = http.client.HTTPConnection(*self.edge_port, timeout=12)
        self.addCleanup(connection.close)
        body = {"model": "gpt-6.1-sol", "input": "fixture-hold-until-release" if hold else "synthetic fixture",
                "stream": True}
        connection.request("POST", "/v1/responses", body=json.dumps(body), headers={
            "Host": "api.fixture.invalid", "Content-Type": "application/json",
            "Authorization": "Bearer " + self.key, "X-Client-Request-ID": marker})
        response = connection.getresponse()
        self.addCleanup(response.close)
        self.assertEqual(response.status, 200)
        self.assertIn("text/event-stream", response.getheader("Content-Type", ""))
        return connection, response

    def release_model(self):
        connection = http.client.HTTPConnection(*self.model_endpoint, timeout=2)
        try:
            connection.request("POST", "/fixture/release", body="")
            response = connection.getresponse()
            response.read(1024)
            self.assertEqual(response.status, 204)
        finally:
            connection.close()

    def test_inflight_sse_finishes_on_draining_owner_and_new_model_reaches_peer_once(self):
        old_connection, old_response = self.model_post("drain-old-stream", hold=True)
        owner_target = old_response.getheader("X-Fixture-Target")
        self.assertIn(owner_target, {app + ":8080" for app in self.apps})
        owner = next(index for index, app in enumerate(self.apps) if app + ":8080" == owner_target)
        peer = 1 - owner
        # Read an actual delta before signaling; a completed/buffered response
        # cannot satisfy the in-flight checkpoint.
        for _ in range(12):
            line = old_response.readline(4096)
            self.assertTrue(line, "held stream must stay open until release")
            if b"response.output_text.delta" in line:
                break
        else:
            self.fail("held model did not emit its first text delta")
        self.assertEqual(self.model_stats(), {"accepted": 1, "completed": 0, "active": 1})
        status = self.fixture_command("status")
        self.assertEqual(status["usage_rows"], 0)
        self.assertEqual(status["dedup_rows"], 0)
        self.assertEqual((status["user_slots"], status["account_slots"], status["api_key_slots"]), (1, 1, 1))
        self.phase("model-sse-held-inflight-with-real-shared-slots")

        bootstrap.docker("kill", "--signal=TERM", self.apps[owner])
        bootstrap.eventually(lambda: self.request(self.ports[owner])[0] == 503, 2,
                             "real owner withdraws readiness before listener closes")
        consecutive = 0

        def peer_admitted():
            nonlocal consecutive
            code, target = self.request(self.edge_port, "/v1/responses", "POST")
            consecutive = consecutive + 1 if code == 401 and target == self.apps[peer] + ":8080" else 0
            return consecutive >= 4

        # These are unauthenticated health-routing observations, not model
        # retries. Only two authenticated model POSTs exist in this scenario.
        bootstrap.eventually(peer_admitted, 3, "Caddy observes drain and routes new requests to peer")
        self.assertEqual(self.model_stats(), {"accepted": 1, "completed": 0, "active": 1})
        self.assertTrue(self.state(self.apps[owner])["Running"], "old SSE owner remains alive while draining")
        new_connection, new_response = self.model_post("drain-new-peer")
        self.assertEqual(new_response.getheader("X-Fixture-Target"), self.apps[peer] + ":8080")
        self.assertIn(b'"response.completed"', new_response.read(65536))
        new_response.close()
        new_connection.close()
        self.assertEqual(self.model_stats(), {"accepted": 2, "completed": 1, "active": 1})
        self.phase("new-authenticated-model-completed-on-peer-old-stream-still-held")
        self.release_model()
        self.assertIn(b'"response.completed"', old_response.read(65536))
        old_response.close()
        old_connection.close()
        wait = bootstrap.docker("wait", self.apps[owner], timeout=20)
        self.assertEqual(wait.stdout.strip(), "0")
        self.assertFalse(self.state(self.apps[owner])["OOMKilled"])
        self.assertTrue(self.state(self.apps[peer])["Running"])

        # The seed/status binary executes inside the peer if app-a was drained.
        self.fixture_executor = self.apps[peer]

        def settled():
            if self.model_stats() != {"accepted": 2, "completed": 2, "active": 0}:
                return False
            status = self.fixture_command("status")
            return (status["usage_rows"] == 2 and status["dedup_rows"] == 2
                    and status["input_tokens"] == 14 and status["output_tokens"] == 4
                    and 9 < status["balance"] < 10
                    and status["user_slots"] == status["account_slots"] == status["api_key_slots"] == 0)

        bootstrap.eventually(settled, 12, "both requests settle once and draining-owner slots release")
        self.report["inflight_drain_verification"] = {"authenticated_posts": 2, "generated": 2,
                                                       "completed": 2, "usage_rows": 2, "dedup_rows": 2,
                                                       "slots": 0, "old_stream_migrated_or_replayed": False,
                                                       "owner_exit_code": 0, "peer_serving": True}
        self.phase("old-model-completed-on-original-owner-once-and-drain-settlement-clean")


class FullApplicationPartialEOFTest(FullApplicationModelSSETest):
    def send_model(self, marker, request_id):
        connection = http.client.HTTPConnection(*self.edge_port, timeout=8)
        self.addCleanup(connection.close)
        connection.request("POST", "/v1/responses", body=json.dumps({
            "model": "gpt-6.1-sol", "input": marker, "stream": True}), headers={
                "Host": "api.fixture.invalid", "Content-Type": "application/json",
                "Authorization": "Bearer " + self.key, "X-Client-Request-ID": request_id,
                "Idempotency-Key": request_id})
        response = connection.getresponse()
        self.addCleanup(response.close)
        self.assertEqual(response.status, 200)
        self.assertIn("text/event-stream", response.getheader("Content-Type", ""))
        target = response.getheader("X-Fixture-Target")
        self.assertIn(target, {app + ":8080" for app in self.apps})
        try:
            body = response.read(65536)
        except http.client.IncompleteRead as error:
            body = error.partial
        response.close()
        connection.close()
        return target, body

    def test_partial_model_sse_eof_is_not_replayed_or_billed_and_peer_can_continue(self):
        failed_target, partial = self.send_model("fixture-partial-sse-eof", "partial-eof-fixture")
        self.assertIn(b'"response.output_text.delta"', partial)
        self.assertIn(b"OK", partial)
        self.assertNotIn(b'"response.completed"', partial)
        self.assertNotIn(b"data: [DONE]", partial)

        def failed_settled():
            if self.model_stats() != {"accepted": 1, "completed": 0, "active": 0}:
                return False
            status = self.fixture_command("status")
            return (status["usage_rows"] == status["dedup_rows"] == 0
                    and status["input_tokens"] == status["output_tokens"] == 0
                    and status["balance"] == 10
                    and status["user_slots"] == status["account_slots"] == status["api_key_slots"] == 0)

        bootstrap.eventually(failed_settled, 12, "partial EOF has one generation, no usage/debit, slots released")
        self.phase("partial-model-eof-one-generation-no-terminal-no-billing-slots-clean")
        # A separate explicit client request, not replay/recovery of the old
        # request; this is exactly the second and last model POST in the case.
        control_target, complete = self.send_model("synthetic control", "partial-eof-control")
        self.assertNotEqual(control_target, failed_target, "healthy round-robin peer must serve the new request")
        self.assertIn(b'"response.completed"', complete)

        def control_settled():
            if self.model_stats() != {"accepted": 2, "completed": 1, "active": 0}:
                return False
            status = self.fixture_command("status")
            return (status["usage_rows"] == status["dedup_rows"] == 1
                    and status["input_tokens"] == 7 and status["output_tokens"] == 2
                    and 9 < status["balance"] < 10
                    and status["user_slots"] == status["account_slots"] == status["api_key_slots"] == 0)

        bootstrap.eventually(control_settled, 12, "peer control settles once, failed partial remains unbilled")
        self.report["partial_eof_verification"] = {"authenticated_posts": 2, "accepted": 2,
                                                    "completed": 1, "failed_request_replayed": False,
                                                    "failed_request_billed": False, "usage_rows": 1,
                                                    "dedup_rows": 1, "slots": 0, "peer_control_completed": True}
        self.phase("new-peer-model-after-partial-eof-settled-once-no-hidden-replay")


class FullApplicationAcceptedEOFTest(FullApplicationModelSSETest):
    def post_model(self, marker, request_id):
        connection = http.client.HTTPConnection(*self.edge_port, timeout=8)
        self.addCleanup(connection.close)
        connection.request("POST", "/v1/responses", body=json.dumps({
            "model": "gpt-6.1-sol", "input": marker, "stream": True}), headers={
                "Host": "api.fixture.invalid", "Content-Type": "application/json",
                "Authorization": "Bearer " + self.key, "X-Client-Request-ID": request_id,
                "Idempotency-Key": request_id})
        response = connection.getresponse()
        self.addCleanup(response.close)
        try:
            payload = response.read(65536)
        except http.client.IncompleteRead as error:
            payload = error.partial
        result = response.status, response.getheader("X-Fixture-Target"), payload
        response.close()
        connection.close()
        return result

    def test_accepted_model_request_eof_is_not_replayed_or_settled_as_success(self):
        # A separate normal control first; exactly two explicit model POSTs.
        # Never warm or recover an endpoint by replaying a model request.
        code, control_target, payload = self.post_model("synthetic control", "accepted-eof-control")
        expected_targets = {app + ":8080" for app in self.apps}
        self.assertEqual(code, 200)
        self.assertIn(control_target, expected_targets)
        self.assertIn(b'"response.completed"', payload)

        def control_settled():
            status = self.fixture_command("status")
            return (self.model_stats() == {"accepted": 1, "completed": 1, "active": 0}
                    and status["usage_rows"] == status["dedup_rows"] == 1
                    and status["input_tokens"] == 7 and status["output_tokens"] == 2
                    and 9 < status["balance"] < 10
                    and status["user_slots"] == status["account_slots"] == status["api_key_slots"] == 0)

        bootstrap.eventually(control_settled, 12, "normal control settled before accepted EOF")
        baseline = self.fixture_command("status")
        self.phase("accepted-eof-control-completed-and-settled-once")
        code, failed_target, payload = self.post_model("fixture-accepted-before-response-eof", "accepted-eof-request")
        self.assertIn(code, (502, 503), "accepted transport failure must be an error, not synthetic success")
        self.assertIn(failed_target, expected_targets)
        self.assertNotEqual(failed_target, control_target)
        self.assertNotIn(b'"response.completed"', payload)
        self.assertNotIn(b"data: [DONE]", payload)

        def failure_settled():
            status = self.fixture_command("status")
            return (self.model_stats() == {"accepted": 2, "completed": 1, "active": 0}
                    and status["usage_rows"] == status["dedup_rows"] == 1
                    and status["input_tokens"] == 7 and status["output_tokens"] == 2
                    and status["balance"] == baseline["balance"]
                    and status["user_slots"] == status["account_slots"] == status["api_key_slots"] == 0)

        bootstrap.eventually(failure_settled, 12, "accepted EOF not regenerated/billed, shared slots released")
        self.report["accepted_eof_verification"] = {"authenticated_posts": 2, "accepted": 2,
                                                     "completed": 1, "usage_rows": 1, "dedup_rows": 1,
                                                     "failed_request_replayed": False,
                                                     "failed_request_billed": False, "slots": 0,
                                                     "eligible_upstream_accounts": 1,
                                                     "scope": "existing single-account pool; no account failover policy change"}
        self.phase("accepted-before-response-eof-once-no-success-billing-slots-clean")


class FullApplicationClientCancelTest(FullApplicationInflightDrainTest):
    def test_client_cancel_drains_existing_upstream_usage_once_and_peer_can_continue(self):
        connection, response = self.model_post("cancel-held-stream", hold=True)
        canceled_target = response.getheader("X-Fixture-Target")
        self.assertIn(canceled_target, {app + ":8080" for app in self.apps})
        for _ in range(12):
            line = response.readline(4096)
            self.assertTrue(line, "client must receive a live SSE before cancellation")
            if line.startswith(b"data: ") and b"response.output_text.delta" in line and b"OK" in line:
                break
        else:
            self.fail("held model did not emit a delta before client cancellation")
        self.assertEqual(self.model_stats(), {"accepted": 1, "completed": 0, "active": 1})
        status = self.fixture_command("status")
        self.assertEqual((status["usage_rows"], status["dedup_rows"]), (0, 0))
        self.assertEqual((status["user_slots"], status["account_slots"], status["api_key_slots"]), (1, 1, 1))
        self.assertEqual(status["balance"], 10)
        self.phase("cancel-case-live-delta-and-shared-slots-before-client-disconnect")

        # Close the real downstream TCP connection, not just stop reading. The
        # existing ordinary passthrough deliberately detaches upstream reads to
        # collect terminal usage after downstream cancellation; do not invent
        # an upstream-abort or free-generation policy in this fixture.
        downstream = connection.sock
        self.assertIsNotNone(downstream, "live SSE must own a cancellable client socket")
        downstream.shutdown(socket.SHUT_RDWR)
        response.close()
        connection.close()
        self.assertTrue(response.isclosed())
        self.assertEqual(downstream.fileno(), -1)
        self.assertEqual(self.model_stats(), {"accepted": 1, "completed": 0, "active": 1})
        status = self.fixture_command("status")
        self.assertEqual((status["usage_rows"], status["dedup_rows"]), (0, 0))
        self.assertEqual((status["input_tokens"], status["output_tokens"]), (0, 0))
        self.assertEqual(status["balance"], 10)
        # Intermediate slot ownership depends on existing admission mode. Only
        # the pre-cancel acquisition and final cleanup are contractual here.
        self.phase("client-socket-closed-existing-upstream-still-held-no-premature-settlement")
        self.release_model()

        def canceled_request_settled():
            status = self.fixture_command("status")
            return (self.model_stats() == {"accepted": 1, "completed": 1, "active": 0}
                    and status["usage_rows"] == status["dedup_rows"] == 1
                    and status["input_tokens"] == 7 and status["output_tokens"] == 2
                    and 9 < status["balance"] < 10
                    and status["user_slots"] == status["account_slots"] == status["api_key_slots"] == 0)

        bootstrap.eventually(canceled_request_settled, 12, "canceled client upstream settles once and slots release")
        canceled_balance = self.fixture_command("status")["balance"]
        self.phase("canceled-client-original-upstream-terminal-usage-settled-once-slots-clean")
        # A distinct explicit request, never a retry of the canceled POST.
        control_connection, control_response = self.model_post("cancel-new-peer-control")
        self.assertIn(control_response.getheader("X-Fixture-Target"), {app + ":8080" for app in self.apps})
        self.assertNotEqual(control_response.getheader("X-Fixture-Target"), canceled_target)
        self.assertIn(b'"response.completed"', control_response.read(65536))
        control_response.close()
        control_connection.close()

        def control_settled():
            status = self.fixture_command("status")
            return (self.model_stats() == {"accepted": 2, "completed": 2, "active": 0}
                    and status["usage_rows"] == status["dedup_rows"] == 2
                    and status["input_tokens"] == 14 and status["output_tokens"] == 4
                    and 9 < status["balance"] < canceled_balance
                    and status["user_slots"] == status["account_slots"] == status["api_key_slots"] == 0)

        bootstrap.eventually(control_settled, 12, "new peer control and canceled upstream each settle once")
        self.report["client_cancel_verification"] = {"authenticated_posts": 2, "accepted": 2,
                                                      "completed": 2, "usage_rows": 2, "dedup_rows": 2,
                                                      "input_tokens": 14, "output_tokens": 4,
                                                      "balance_before": 10, "balance_after_canceled": canceled_balance,
                                                      "balance_final": self.fixture_command("status")["balance"],
                                                      "client_socket_closed": True,
                                                      "canceled_request_replayed": False, "slots": 0,
                                                      "scope": "existing detached usage drain; no upstream cancellation policy change"}
        self.phase("new-peer-control-after-client-cancel-unique-billing-and-cleanup")


class FullApplicationConcurrentFailureTest(FullApplicationInflightDrainTest):
    def failed_model_post(self):
        connection = http.client.HTTPConnection(*self.edge_port, timeout=12)
        self.addCleanup(connection.close)
        connection.request("POST", "/v1/responses", body=json.dumps({
            "model": "gpt-6.1-sol", "input": "fixture-held-partial-sse-eof", "stream": True}), headers={
                "Host": "api.fixture.invalid", "Content-Type": "application/json",
                "Authorization": "Bearer " + self.key, "X-Client-Request-ID": "concurrent-failure"})
        response = connection.getresponse()
        self.addCleanup(response.close)
        self.assertEqual(response.status, 200)
        self.assertIn("text/event-stream", response.getheader("Content-Type", ""))
        return connection, response

    def read_live_delta(self, response):
        for _ in range(12):
            line = response.readline(4096)
            self.assertTrue(line, "both concurrent requests must stay live until their own release")
            if line.startswith(b"data: ") and b"response.output_text.delta" in line and b"OK" in line:
                return
        self.fail("concurrent held stream did not emit its real text delta")

    def test_failed_concurrent_stream_releases_only_its_lease_and_peer_settles_once(self):
        success_connection, success_response = self.model_post("concurrent-success", hold=True)
        self.read_live_delta(success_response)
        failed_connection, failed_response = self.failed_model_post()
        self.read_live_delta(failed_response)
        expected = {app + ":8080" for app in self.apps}
        targets = {success_response.getheader("X-Fixture-Target"), failed_response.getheader("X-Fixture-Target")}
        self.assertEqual(targets, expected, "two overlapping requests must reach different real applications")
        self.assertEqual(self.model_stats(), {"accepted": 2, "completed": 0, "active": 2})
        status = self.fixture_command("status")
        self.assertEqual((status["user_slots"], status["account_slots"], status["api_key_slots"]), (2, 2, 2))
        self.assertEqual((status["usage_rows"], status["dedup_rows"]), (0, 0))
        self.assertEqual((status["input_tokens"], status["output_tokens"]), (0, 0))
        self.assertEqual(status["balance"], 10)
        self.phase("two-full-app-models-overlap-with-two-shared-leases-no-early-billing")

        control = http.client.HTTPConnection(*self.model_endpoint, timeout=2)
        try:
            control.request("POST", "/fixture/release-failure", body="")
            result = control.getresponse()
            self.addCleanup(result.close)
            self.assertEqual(result.status, 204)
            result.read(1024)
            result.close()
        finally:
            control.close()
        try:
            failed_payload = failed_response.read(65536)
        except http.client.IncompleteRead as error:
            failed_payload = error.partial
        self.assertNotIn(b'"response.completed"', failed_payload)
        self.assertNotIn(b"data: [DONE]", failed_payload)
        failed_response.close()
        failed_connection.close()

        def failed_lease_released():
            status = self.fixture_command("status")
            return (self.model_stats() == {"accepted": 2, "completed": 0, "active": 1}
                    and status["usage_rows"] == status["dedup_rows"] == 0
                    and status["input_tokens"] == status["output_tokens"] == 0
                    and status["balance"] == 10
                    and status["user_slots"] == status["account_slots"] == status["api_key_slots"] == 1)

        bootstrap.eventually(failed_lease_released, 6, "failed request releases only its lease without billing peer")
        self.assertTrue(all(self.state(app)["Running"] for app in self.apps))
        self.phase("failed-stream-unbilled-peer-still-held-one-shared-lease-remains")
        self.release_model()
        self.assertIn(b'"response.completed"', success_response.read(65536))
        success_response.close()
        success_connection.close()

        def peer_settled():
            status = self.fixture_command("status")
            return (self.model_stats() == {"accepted": 2, "completed": 1, "active": 0}
                    and status["usage_rows"] == status["dedup_rows"] == 1
                    and status["input_tokens"] == 7 and status["output_tokens"] == 2
                    and 9 < status["balance"] < 10
                    and status["user_slots"] == status["account_slots"] == status["api_key_slots"] == 0)

        bootstrap.eventually(peer_settled, 12, "successful peer settles once after failed concurrent request")
        self.report["concurrent_failure_verification"] = {"authenticated_posts": 2, "accepted": 2,
                                                          "completed": 1, "usage_rows": 1, "dedup_rows": 1,
                                                          "input_tokens": 7, "output_tokens": 2,
                                                          "balance_before": 10,
                                                          "balance_final": self.fixture_command("status")["balance"],
                                                          "slots_before_failure": 2, "slots_after_failure": 1,
                                                          "slots_final": 0, "failed_request_replayed": False,
                                                          "failed_request_billed": False,
                                                          "scope": "overlapping failed/successful requests; not concurrent duplicate billing identities"}
        self.phase("concurrent-failure-peer-completed-once-unique-billing-all-leases-clean")


class FullApplicationConcurrentSuccessTest(FullApplicationConcurrentFailureTest):
    def test_two_held_successes_release_together_and_preserve_both_settlements(self):
        first_connection, first_response = self.model_post("concurrent-success-first", hold=True)
        self.read_live_delta(first_response)
        second_connection, second_response = self.model_post("concurrent-success-second", hold=True)
        self.read_live_delta(second_response)
        self.assertEqual({first_response.getheader("X-Fixture-Target"),
                          second_response.getheader("X-Fixture-Target")},
                         {app + ":8080" for app in self.apps},
                         "two successful in-flight requests must belong to different real applications")
        self.assertEqual(self.model_stats(), {"accepted": 2, "completed": 0, "active": 2})
        status = self.fixture_command("status")
        self.assertEqual((status["user_slots"], status["account_slots"], status["api_key_slots"]), (2, 2, 2))
        self.assertEqual((status["usage_rows"], status["dedup_rows"]), (0, 0))
        self.assertEqual((status["input_tokens"], status["output_tokens"]), (0, 0))
        self.assertEqual(status["balance"], 10)
        self.phase("two-real-app-successful-streams-held-shared-slots-two-before-settlement")

        # A single existing control releases both independently held upstreams.
        # Read order below does not serialize upstream completion or app usage
        # workers, but no claim is made that SQL transactions overlap exactly.
        self.release_model()
        for connection, response in ((first_connection, first_response), (second_connection, second_response)):
            payload = response.read(65536)
            self.assertIn(b'"response.completed"', payload)
            response.close()
            connection.close()
        self.phase("single-release-allows-both-original-app-streams-to-complete-without-replay")

        # Fixture rate=1: each response has 7 input + 2 output tokens, priced at
        # 0.000001 and 0.000002. Compare decimal values at the real ledger scale;
        # a broad nonzero-debit check would miss a lost or repeated balance write.
        per_request = Decimal(7) * Decimal("0.000001") + Decimal(2) * Decimal("0.000002")
        expected_balance = Decimal("10") - 2 * per_request

        def both_settled():
            status = self.fixture_command("status")
            return (self.model_stats() == {"accepted": 2, "completed": 2, "active": 0}
                    and status["usage_rows"] == status["dedup_rows"] == 2
                    and status["input_tokens"] == 14 and status["output_tokens"] == 4
                    and Decimal(str(status["balance"])) == expected_balance
                    and status["user_slots"] == status["account_slots"] == status["api_key_slots"] == 0)

        bootstrap.eventually(both_settled, 12, "both app settlements preserve exact cumulative debit and release slots")
        self.assertTrue(all(self.state(app)["Running"] for app in self.apps))
        self.report["concurrent_success_verification"] = {"authenticated_posts": 2, "accepted": 2,
                                                          "completed": 2, "usage_rows": 2, "dedup_rows": 2,
                                                          "input_tokens": 14, "output_tokens": 4,
                                                          "balance_before": 10,
                                                          "balance_expected": str(expected_balance),
                                                          "balance_final": self.fixture_command("status")["balance"],
                                                          "slots_before_release": 2, "slots_final": 0,
                                                          "requests_replayed": False,
                                                          "scope": "two held successes released together; not forced SQL transaction overlap"}
        self.phase("both-successful-app-requests-settled-once-exact-debit-all-slots-clean")


class FullApplicationMultiAccountEOFTest(FullApplicationAcceptedEOFTest):
    def fixture_command(self, operation):
        mapped = {"seed": "seed-multi", "status": "status-multi"}.get(operation, operation)
        value = super().fixture_command(mapped)
        if operation == "seed":
            self.assertGreater(value["peer_account_id"], 0)
            self.assertNotEqual(value["peer_account_id"], value["account_id"])
            self.fixture_input["peer_account_id"] = value["peer_account_id"]
        return value

    def multi_stats(self):
        connection = http.client.HTTPConnection(*self.model_endpoint, timeout=2)
        try:
            connection.request("GET", "/fixture/multi-stats")
            response = connection.getresponse()
            self.addCleanup(response.close)
            self.assertEqual(response.status, 200)
            result = json.loads(response.read(4096))
            response.close()
            attempts = {"primary_http_attempts": result.pop("primary_http_attempts"),
                        "peer_http_attempts": result.pop("peer_http_attempts")}
            self.assertEqual(attempts["primary_http_attempts"], result["primary_accepted"],
                             "all observed primary HTTP attempts in this fixture must be accounted for")
            self.assertEqual(attempts["peer_http_attempts"], result["peer_accepted"],
                             "all observed peer HTTP attempts in this fixture must be accounted for")
            observation = {**attempts, **result,
                           "scope": "synthetic upstream HTTP arrivals; excludes pre-arrival network failures"}
            self.report["observed_multi_account_http_attempts"] = observation
            if getattr(self, "last_attempt_observation", None) != observation:
                print("isolated multi-account attempt observation: " + json.dumps(observation), flush=True)
                self.last_attempt_observation = observation
            return result
        finally:
            connection.close()

    def test_existing_multi_account_failover_can_accept_again_after_ambiguous_eof(self):
        self.assertEqual(self.multi_stats(), {"primary_accepted": 0, "peer_accepted": 0,
                                             "primary_completed": 0, "peer_completed": 0})
        # Exactly ONE client POST. Whichever legal account is selected first
        # fully accepts it then closes before any response. The next distinct
        # account completes under the existing unmodified application policy.
        # This characterizes reacceptance; it must never be labeled no replay.
        code, target, payload = self.post_model("fixture-multi-account-accepted-eof", "multi-account-eof-once")
        self.assertEqual(code, 200)
        self.assertIn(target, {app + ":8080" for app in self.apps})
        self.assertIn(b'"response.completed"', payload)
        self.phase("one-client-post-existing-failover-completes-after-first-accepted-response-eof")

        def settled():
            counts = self.multi_stats()
            status = self.fixture_command("status")
            return (self.model_stats() == {"accepted": 2, "completed": 1, "active": 0}
                    and counts["primary_accepted"] == counts["peer_accepted"] == 1
                    and counts["primary_completed"] + counts["peer_completed"] == 1
                    and status["usage_rows"] == counts["primary_completed"]
                    and status["peer_usage_rows"] == counts["peer_completed"]
                    and status["dedup_rows"] == 1
                    and status["input_tokens"] == 7 * counts["primary_completed"]
                    and status["output_tokens"] == 2 * counts["primary_completed"]
                    and status["peer_input_tokens"] == 7 * counts["peer_completed"]
                    and status["peer_output_tokens"] == 2 * counts["peer_completed"]
                    and status["balance"] == 9.999989
                    and status["user_slots"] == status["api_key_slots"] == 0
                    and status["account_slots"] == status["peer_account_slots"] == 0)

        bootstrap.eventually(settled, 12, "existing two-account failover reaccepts once and settles observed usage only")
        self.assertTrue(all(self.state(app)["Running"] for app in self.apps))
        counts = self.multi_stats()
        status = self.fixture_command("status")
        self.report["multi_account_eof_verification"] = {"authenticated_client_posts": 1,
                                                        "eligible_upstream_accounts": 2,
                                                        "upstream_acceptances": 2, "observed_completions": 1,
                                                        "primary_accepted": counts["primary_accepted"],
                                                        "peer_accepted": counts["peer_accepted"],
                                                        "primary_completed": counts["primary_completed"],
                                                        "peer_completed": counts["peer_completed"],
                                                        "primary_usage_rows": status["usage_rows"],
                                                        "peer_usage_rows": status["peer_usage_rows"],
                                                        "dedup_rows": 1, "input_tokens": 7, "output_tokens": 2,
                                                        "balance_final": status["balance"], "all_slots_final": 0,
                                                        "automatic_account_reacceptance_observed": True,
                                                        "first_attempt_external_billing": "not modeled; cannot infer from local usage",
                                                        "scope": "existing failover characterization; not an at-most-once guarantee"}
        self.phase("two-distinct-accounts-accepted-one-client-post-observed-local-settlement-slots-clean")



class FullApplicationMultiAccountPartialEOFTest(FullApplicationMultiAccountEOFTest):
    def test_existing_multi_account_policy_stops_after_partial_semantic_output_eof(self):
        self.assertEqual(self.multi_stats(), {"primary_accepted": 0, "peer_accepted": 0,
                                             "primary_completed": 0, "peer_completed": 0})
        # The same two eligible account pool as the pre-response EOF case, but
        # this upstream flushes semantic output first. No policy is toggled to
        # manufacture a no-failover result; one client POST is the whole case.
        code, target, partial = self.post_model("fixture-multi-account-partial-eof", "multi-account-partial-once")
        self.assertEqual(code, 200)
        self.assertIn(target, {app + ":8080" for app in self.apps})
        self.assertIn(b'"response.output_text.delta"', partial)
        self.assertIn(b"OK", partial)
        self.assertNotIn(b'"response.completed"', partial)
        self.assertNotIn(b"data: [DONE]", partial)
        self.phase("multi-account-partial-eof-client-received-real-delta-without-terminal")

        def settled():
            counts = self.multi_stats()
            status = self.fixture_command("status")
            return (self.model_stats() == {"accepted": 1, "completed": 0, "active": 0}
                    and counts["primary_accepted"] + counts["peer_accepted"] == 1
                    and counts["primary_completed"] == counts["peer_completed"] == 0
                    and status["usage_rows"] == status["peer_usage_rows"] == status["dedup_rows"] == 0
                    and status["input_tokens"] == status["output_tokens"] == 0
                    and status["peer_input_tokens"] == status["peer_output_tokens"] == 0
                    and status["balance"] == 10
                    and status["user_slots"] == status["api_key_slots"] == 0
                    and status["account_slots"] == status["peer_account_slots"] == 0)

        bootstrap.eventually(settled, 12, "semantic partial EOF does not switch to second eligible account or settle success")
        self.assertTrue(all(self.state(app)["Running"] for app in self.apps))
        counts = self.multi_stats()
        self.report["multi_account_partial_eof_verification"] = {"authenticated_client_posts": 1,
                                                                "eligible_upstream_accounts": 2,
                                                                "upstream_acceptances": 1, "observed_completions": 0,
                                                                "primary_accepted": counts["primary_accepted"],
                                                                "peer_accepted": counts["peer_accepted"],
                                                                "primary_usage_rows": 0, "peer_usage_rows": 0,
                                                                "dedup_rows": 0, "input_tokens": 0, "output_tokens": 0,
                                                                "balance_final": self.fixture_command("status")["balance"],
                                                                "all_slots_final": 0,
                                                                "automatic_account_reacceptance_observed": False,
                                                                "first_attempt_external_billing": "not modeled; cannot infer from local usage",
                                                                "scope": "existing semantic-output boundary; not a universal at-most-once guarantee"}
        self.phase("partial-semantic-output-eof-one-account-accepted-no-local-success-settlement-slots-clean")



class FullApplicationQueuedCancelTest(FullApplicationConcurrentSuccessTest):
    def test_third_queued_client_cancels_before_generation_without_releasing_two_active_peers(self):
        first_connection, first_response = self.model_post("queue-active-first", hold=True)
        self.read_live_delta(first_response)
        second_connection, second_response = self.model_post("queue-active-second", hold=True)
        self.read_live_delta(second_response)
        self.assertEqual({first_response.getheader("X-Fixture-Target"), second_response.getheader("X-Fixture-Target")},
                         {app + ":8080" for app in self.apps})
        self.assertEqual(self.model_stats(), {"accepted": 2, "completed": 0, "active": 2})
        status = self.fixture_command("status")
        self.assertEqual((status["user_slots"], status["account_slots"], status["api_key_slots"]), (2, 2, 2))
        self.assertEqual((status["api_key_queue_active"], status["api_key_queue_waiting"]), (2, 0))
        self.phase("two-real-app-held-streams-fill-key-user-account-capacity-before-third-request")

        queued = http.client.HTTPConnection(*self.edge_port, timeout=8)
        outcome = {"returned_response": False, "disconnected": False}
        reader = None

        def cancel_and_join():
            # Run before container teardown, including any assertion/timeout
            # failure. No daemon thread may retain a response/socket silently.
            if queued.sock is not None:
                try:
                    queued.sock.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass  # The canceled peer may already have closed it.
            queued.close()
            if reader is not None:
                reader.join(timeout=2)
                self.assertFalse(reader.is_alive(), "queued response reader cleanup exceeded budget")

        self.addCleanup(cancel_and_join)
        queued.request("POST", "/v1/responses", body=json.dumps({
            "model": "gpt-6.1-sol", "input": "synthetic queued request must never reach model", "stream": True}),
            headers={"Host": "api.fixture.invalid", "Content-Type": "application/json",
                     "Authorization": "Bearer " + self.key, "X-Client-Request-ID": "queue-cancel-third"})

        def receive_queued_response():
            try:
                response = queued.getresponse()
                outcome["returned_response"] = True
                response.close()
            except (OSError, http.client.HTTPException):
                # Export only fixed facts, never connection/error details.
                outcome["disconnected"] = True
            finally:
                queued.close()

        reader = threading.Thread(target=receive_queued_response, daemon=True)
        reader.start()

        def third_waiting():
            status = self.fixture_command("status")
            return (self.model_stats() == {"accepted": 2, "completed": 0, "active": 2}
                    and status["api_key_queue_active"] == 2 and status["api_key_queue_waiting"] == 1
                    and status["user_slots"] == status["account_slots"] == status["api_key_slots"] == 2
                    and status["usage_rows"] == status["dedup_rows"] == 0 and status["balance"] == 10
                    and reader.is_alive() and not outcome["returned_response"])

        bootstrap.eventually(third_waiting, 3, "third real request is queued in shared Redis before upstream admission")
        self.phase("third-authenticated-client-is-in-key-queue-no-extra-generation-or-user-account-slot")
        self.assertIsNotNone(queued.sock)
        queued.sock.shutdown(socket.SHUT_RDWR)
        reader.join(timeout=2)
        self.assertFalse(reader.is_alive(), "canceled queued HTTP reader must terminate")
        self.assertTrue(outcome["disconnected"])
        self.assertFalse(outcome["returned_response"])

        def canceled_waiter_removed():
            status = self.fixture_command("status")
            return (self.model_stats() == {"accepted": 2, "completed": 0, "active": 2}
                    and status["api_key_queue_active"] == 2 and status["api_key_queue_waiting"] == 0
                    and status["user_slots"] == status["account_slots"] == status["api_key_slots"] == 2
                    and status["usage_rows"] == status["dedup_rows"] == 0
                    and status["input_tokens"] == status["output_tokens"] == 0 and status["balance"] == 10)

        bootstrap.eventually(canceled_waiter_removed, 3, "canceled waiter removed without releasing active peer reservations")
        self.phase("canceled-queued-client-never-reached-upstream-two-active-leases-intact")
        self.release_model()
        for connection, response in ((first_connection, first_response), (second_connection, second_response)):
            self.assertIn(b'"response.completed"', response.read(65536))
            response.close()
            connection.close()

        def active_peers_settled():
            status = self.fixture_command("status")
            return (self.model_stats() == {"accepted": 2, "completed": 2, "active": 0}
                    and status["usage_rows"] == status["dedup_rows"] == 2
                    and status["input_tokens"] == 14 and status["output_tokens"] == 4
                    and Decimal(str(status["balance"])) == Decimal("9.999978")
                    and status["user_slots"] == status["account_slots"] == status["api_key_slots"] == 0
                    and status["api_key_queue_active"] == status["api_key_queue_waiting"] == 0)

        bootstrap.eventually(active_peers_settled, 12, "only two admitted peers settle and shared queue/slots finish empty")
        self.report["queued_cancel_verification"] = {"authenticated_client_posts": 3,
                                                      "upstream_acceptances": 2, "completed": 2,
                                                      "queue_waiting_before_cancel": 1, "queue_waiting_after_cancel": 0,
                                                      "active_peer_slots_after_cancel": 2,
                                                      "usage_rows": 2, "dedup_rows": 2,
                                                      "input_tokens": 14, "output_tokens": 4,
                                                      "balance_final": self.fixture_command("status")["balance"],
                                                      "all_slots_final": 0, "queue_final": 0,
                                                      "queued_client_reader_joined": True,
                                                      "scope": "existing queue cancellation before upstream; not timeout/full-queue/revocation policy"}
        self.phase("queued-cancel-no-hidden-generation-two-peers-unique-billing-all-capacity-clean")


if __name__ == "__main__":
    # The original bootstrap suite still runs as its existing CI step. This
    # Explicit cases avoid rerunning inherited tests as accidental duplicates.
    suite = unittest.TestSuite([
        FullApplicationModelSSETest("test_two_real_applications_complete_sse_once_and_settle_shared_state"),
        FullApplicationInflightDrainTest("test_inflight_sse_finishes_on_draining_owner_and_new_model_reaches_peer_once"),
        FullApplicationPartialEOFTest("test_partial_model_sse_eof_is_not_replayed_or_billed_and_peer_can_continue"),
        FullApplicationAcceptedEOFTest("test_accepted_model_request_eof_is_not_replayed_or_settled_as_success"),
        FullApplicationClientCancelTest("test_client_cancel_drains_existing_upstream_usage_once_and_peer_can_continue"),
        FullApplicationConcurrentFailureTest("test_failed_concurrent_stream_releases_only_its_lease_and_peer_settles_once"),
        FullApplicationConcurrentSuccessTest("test_two_held_successes_release_together_and_preserve_both_settlements"),
        FullApplicationMultiAccountEOFTest("test_existing_multi_account_failover_can_accept_again_after_ambiguous_eof"),
        FullApplicationMultiAccountPartialEOFTest("test_existing_multi_account_policy_stops_after_partial_semantic_output_eof"),
        FullApplicationQueuedCancelTest("test_third_queued_client_cancels_before_generation_without_releasing_two_active_peers")])
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    raise SystemExit(0 if result.wasSuccessful() else 1)
