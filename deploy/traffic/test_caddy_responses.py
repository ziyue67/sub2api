"""Real pinned Caddy with two local, synthetic HTTP/SSE origins; CI only."""

import http.client
import json
import os
from pathlib import Path
import select
import socket
import ssl
import subprocess
import tempfile
import threading
import time
import unittest
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


ORIGIN_HOST = "api.fixture.invalid"
TEMPLATE = Path(__file__).with_name("Caddyfile.responses")


def eventually(predicate, timeout=8):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.025)
    raise AssertionError("isolated proxy condition did not converge within budget")


class OriginState:
    def __init__(self, name):
        self.name = name
        self.peek_flags = socket.MSG_PEEK
        self.lock = threading.Lock()
        self.ready = True
        self.health_checks = 0
        self.wrong_hosts = 0
        self.calls = {}
        self.active = set()
        self.cancelled = set()
        self.releases = {}
        self.stop = threading.Event()

    def count(self, token):
        with self.lock:
            return self.calls.get(token, 0)

    def set_ready(self, ready):
        with self.lock:
            self.ready = ready
            return self.health_checks


def origin_handler(state):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *_args):
            pass

        def reply(self, status, payload=b""):
            self.send_response(status)
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def do_GET(self):
            if self.path != "/readyz":
                self.reply(404)
                return
            with state.lock:
                state.health_checks += 1
                correct_host = self.headers.get("Host") == ORIGIN_HOST
                state.wrong_hosts += not correct_host
                ready = state.ready and correct_host
            self.reply(200 if ready else 503, b"ready" if ready else b"not ready")

        def do_POST(self):
            size = int(self.headers.get("Content-Length", "0"))
            payload = json.loads(self.rfile.read(size))
            token = self.headers["X-Fixture-ID"]
            mode = payload.get("fixture_mode", "normal")
            with state.lock:
                state.calls[token] = state.calls.get(token, 0) + 1
                correct_host = self.headers.get("Host") == ORIGIN_HOST
                state.wrong_hosts += not correct_host
            if not correct_host:
                self.reply(421)
                return
            if mode == "reject":
                self.reply(429, b'{"error":"synthetic rejection"}')
                return
            if mode == "eof":
                self.close_connection = True
                self.connection.shutdown(socket.SHUT_RDWR)
                return
            if mode not in ("hold", "partial"):
                self.reply(200, json.dumps({"node": state.name}).encode())
                return
            release = threading.Event()
            with state.lock:
                state.active.add(token)
                state.releases[token] = release
            try:
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Connection", "close")
                self.end_headers()
                self.close_connection = True
                self.wfile.write(b'data: {"fixture":"first"}\n\n')
                self.wfile.flush()
                if mode == "partial":
                    self.connection.shutdown(socket.SHUT_RDWR)
                    return
                if state.peek_flags == 0:
                    self.connection.settimeout(0.2)  # TLS recv cannot use MSG_PEEK.
                while not release.is_set() and not state.stop.is_set():
                    readable, _, _ = select.select([self.connection], [], [], 0.05)
                    try:
                        closed = readable and self.connection.recv(1, state.peek_flags) == b""
                    except TimeoutError:
                        continue
                    if closed:
                        with state.lock:
                            state.cancelled.add(token)
                        return
                if release.is_set():
                    self.wfile.write(b"data: [DONE]\n\n")
                    self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError, ssl.SSLError):
                with state.lock:
                    state.cancelled.add(token)
            finally:
                with state.lock:
                    state.active.discard(token)
                    state.releases.pop(token, None)

    return Handler


class CaddyResponsesTest(unittest.TestCase):
    template = TEMPLATE

    def prepare_origins(self):
        pass

    def configure_origin(self, server, state):
        pass

    def extra_environment(self):
        return {}

    def setUp(self):
        self.binary = os.environ["CADDY_BIN"]
        version = subprocess.check_output([self.binary, "version"], text=True, timeout=5)
        self.assertTrue(version.startswith("v2.11.7 "), "CI must use the pinned binary")
        self.temp = tempfile.TemporaryDirectory(prefix="s2-caddy-")
        self.addCleanup(self.temp.cleanup)
        self.states = [OriginState("a"), OriginState("b")]
        self.prepare_origins()
        self.origins = []
        for state in self.states:
            server = ThreadingHTTPServer(("127.0.0.1", 0), origin_handler(state))
            server.daemon_threads = True
            try:
                self.configure_origin(server, state)
            except Exception:
                server.server_close()
                raise
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            self.origins.append(server)
            self.addCleanup(self.close_origin, server, state, thread)
        with socket.socket() as reservation:
            reservation.bind(("127.0.0.1", 0))
            self.port = reservation.getsockname()[1]
        env = os.environ.copy()
        env.update({
            "SUB2API_LB_LISTEN": "127.0.0.1:" + str(self.port),
            "SUB2API_LB_UPSTREAM_A": "127.0.0.1:" + str(self.origins[0].server_port),
            "SUB2API_LB_UPSTREAM_B": "127.0.0.1:" + str(self.origins[1].server_port),
            "SUB2API_LB_ORIGIN_HOST": ORIGIN_HOST,
            "SUB2API_LB_HEALTH_INTERVAL": "100ms",
            "XDG_DATA_HOME": self.temp.name,
            "XDG_CONFIG_HOME": self.temp.name,
        })
        env.update(self.extra_environment())
        self.proxy_env = env
        self.log = open(Path(self.temp.name) / "caddy.log", "wb")
        self.addCleanup(self.log.close)
        self.start_caddy(env)
        eventually(lambda: all(s.health_checks >= 2 for s in self.states))

    def start_caddy(self, env):
        # Parser errors may be printed in CI; this configuration contains only
        # synthetic local fixture addresses and no credentials/request bodies.
        subprocess.run([self.binary, "validate", "--config", str(self.template), "--adapter", "caddyfile"],
                       env=env, check=True, timeout=10)
        self.caddy = subprocess.Popen(
            [self.binary, "run", "--config", str(self.template), "--adapter", "caddyfile"],
            env=env, stdout=self.log, stderr=self.log)
        self.addCleanup(self.close_caddy)
        eventually(self.listening)

    def listening(self):
        self.assertIsNone(self.caddy.poll(), "Caddy must stay alive")
        try:
            with socket.create_connection(("127.0.0.1", self.port), timeout=0.1):
                return True
        except OSError:
            return False

    def close_caddy(self):
        if self.caddy.poll() is None:
            self.caddy.terminate()
            try:
                self.caddy.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.caddy.kill()
                self.caddy.wait(timeout=5)

    def close_origin(self, server, state, thread):
        state.stop.set()
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)
        self.assertFalse(thread.is_alive())
        eventually(lambda: not state.active, timeout=2)
        self.assertEqual(state.wrong_hosts, 0)

    def request(self, mode="normal", path="/v1/responses", method="POST"):
        token = uuid.uuid4().hex
        connection = http.client.HTTPConnection("127.0.0.1", self.port, timeout=3)
        self.addCleanup(connection.close)
        payload = json.dumps({"fixture_mode": mode}) if method == "POST" else None
        connection.request(method, path, body=payload, headers={
            "Content-Type": "application/json", "X-Fixture-ID": token,
            # Even callers that supply an idempotency hint must not be replayed.
            "Idempotency-Key": token,
        })
        response = connection.getresponse()
        self.addCleanup(response.close)
        return token, connection, response

    def total(self, token):
        return sum(state.count(token) for state in self.states)

    def only(self, index):
        previous = [s.set_ready(i == index) for i, s in enumerate(self.states)]
        eventually(lambda: all(s.health_checks >= n + 3 for s, n in zip(self.states, previous)))
        consecutive = 0

        def converged():
            nonlocal consecutive
            _, connection, response = self.request()
            payload = response.read()
            connection.close()
            if response.status == 200 and json.loads(payload)["node"] == self.states[index].name:
                consecutive += 1
            else:
                consecutive = 0
            return consecutive >= 6

        eventually(converged)

    def test_both_origins_and_restricted_routes(self):
        nodes = set()
        for path in ("/v1/responses", "/v1/responses/compact") * 4:
            token, connection, response = self.request(path=path)
            self.assertEqual(response.status, 200)
            nodes.add(json.loads(response.read())["node"])
            connection.close()
            self.assertEqual(self.total(token), 1)
        self.assertEqual(nodes, {"a", "b"})
        for method, path in (("GET", "/v1/responses"), ("POST", "/api/v1/admin/users"),
                             ("POST", "/api/v1/payment"), ("GET", "/api/bps-images/fixture")):
            token, _, response = self.request(path=path, method=method)
            self.assertEqual(response.status, 404)
            response.read()
            self.assertEqual(self.total(token), 0)

    def test_readiness_withdrawal_and_recovery(self):
        self.only(1)
        for _ in range(6):
            token, _, response = self.request()
            self.assertEqual(response.status, 200)
            self.assertEqual(json.loads(response.read())["node"], "b")
            self.assertEqual(self.states[0].count(token), 0)
        previous = [s.set_ready(False) for s in self.states]
        eventually(lambda: all(s.health_checks >= n + 3 for s, n in zip(self.states, previous)))

        def withdrawn():
            token, _, response = self.request()
            response.read()
            return response.status == 503 and self.total(token) == 0

        eventually(withdrawn)
        self.only(0)
        token, _, response = self.request()
        self.assertEqual(json.loads(response.read())["node"], "a")
        self.assertEqual(self.total(token), 1)

    def test_drain_changes_only_new_requests(self):
        self.only(0)
        token, connection, response = self.request("hold")
        self.assertEqual(response.status, 200)
        self.assertIn(b"first", response.readline())  # Arrives before origin completes.
        self.assertIn(token, self.states[0].active)
        self.only(1)  # Origin A now returns readiness 503 while its old stream lives.
        self.assertIn(token, self.states[0].active)
        self.assertNotIn(token, self.states[0].cancelled)
        new_token, _, new_response = self.request()
        self.assertEqual(json.loads(new_response.read())["node"], "b")
        self.assertEqual(self.states[0].count(new_token), 0)
        with self.states[0].lock:
            self.states[0].releases[token].set()
        self.assertIn(b"[DONE]", response.read())
        connection.close()
        eventually(lambda: token not in self.states[0].active)
        self.assertEqual(self.total(token), 1)

    def test_accepted_post_eof_is_not_replayed(self):
        token, _, response = self.request("eof")
        self.assertEqual(response.status, 502)
        response.read()
        self.assertEqual(self.total(token), 1)

    def test_partial_sse_is_not_replayed(self):
        token, _, response = self.request("partial")
        self.assertEqual(response.status, 200)
        self.assertIn(b"first", response.readline())
        try:
            rest = response.read()
        except http.client.IncompleteRead as error:
            rest = error.partial
        self.assertNotIn(b"[DONE]", rest)
        self.assertEqual(self.total(token), 1)
        eventually(lambda: not any(token in s.active for s in self.states))

    def test_upstream_rejection_is_not_replayed(self):
        token, _, response = self.request("reject")
        self.assertEqual(response.status, 429)
        response.read()
        self.assertEqual(self.total(token), 1)

    def test_client_cancel_reaches_origin_and_cleans_stream(self):
        token, connection, response = self.request("hold")
        self.assertEqual(response.status, 200)
        self.assertIn(b"first", response.readline())
        response.close()
        connection.close()
        eventually(lambda: any(token in s.cancelled for s in self.states))
        eventually(lambda: not any(token in s.active for s in self.states))
        self.assertEqual(self.total(token), 1)


if __name__ == "__main__":
    unittest.main(verbosity=2)
