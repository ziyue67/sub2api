"""CI-only verified TLS origins; synthetic certificates never leave the fixture."""

import json
from pathlib import Path
import ssl
import subprocess
import tempfile
import unittest

import test_caddy_responses as plaintext


class CaddyTLSResponsesTest(plaintext.CaddyResponsesTest):
    template = Path(__file__).with_name("Caddyfile.responses-tls")

    @classmethod
    def setUpClass(cls):
        cls.certificates = tempfile.TemporaryDirectory(prefix="s2-origin-certs-")
        cls.addClassCleanup(cls.certificates.cleanup)
        directory = Path(cls.certificates.name)
        cls.ca = directory / "ca.pem"
        cls.wrong_ca = directory / "unrelated-ca.pem"
        cls.cert = directory / "origin.pem"
        cls.key = directory / "origin.key"

        def openssl(*args):
            # Only fixture filenames/names enter argv; never print generated keys.
            subprocess.run(["openssl", *map(str, args)], check=True, timeout=15,
                           stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)

        for name, certificate in (("fixture-root", cls.ca), ("unrelated-root", cls.wrong_ca)):
            openssl("req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
                    "-subj", "/CN=" + name, "-keyout", directory / (name + ".key"),
                    "-out", certificate, "-addext", "basicConstraints=critical,CA:TRUE",
                    "-addext", "keyUsage=critical,keyCertSign,cRLSign")
        request = directory / "origin.csr"
        openssl("req", "-new", "-newkey", "rsa:2048", "-nodes",
                "-subj", "/CN=" + plaintext.ORIGIN_HOST, "-keyout", cls.key, "-out", request)
        extensions = directory / "origin.ext"
        extensions.write_text("subjectAltName=DNS:" + plaintext.ORIGIN_HOST + "\n"
                              "basicConstraints=critical,CA:FALSE\n"
                              "keyUsage=critical,digitalSignature,keyEncipherment\n"
                              "extendedKeyUsage=serverAuth\n")
        openssl("x509", "-req", "-in", request, "-CA", cls.ca,
                "-CAkey", directory / "fixture-root.key", "-CAcreateserial",
                "-out", cls.cert, "-days", "1", "-extfile", extensions)

    def prepare_origins(self):
        for state in self.states:
            state.peek_flags = 0
            state.handshakes = []

    def configure_origin(self, server, state):
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.load_cert_chain(self.cert, self.key)

        def observe_sni(_socket, name, _context):
            with state.lock:
                state.handshakes.append(name)

        context.set_servername_callback(observe_sni)
        server.socket = context.wrap_socket(server.socket, server_side=True)

    def extra_environment(self):
        return {"SUB2API_LB_TLS_SERVER_NAME": plaintext.ORIGIN_HOST,
                "SUB2API_LB_TRUSTED_CA_FILE": str(self.ca)}

    def test_tls_ca_sni_and_health_host(self):
        # Actual IP connections must verify the DNS SAN, for probes AND business.
        for _ in range(4):
            token, _, response = self.request()
            self.assertEqual(response.status, 200)
            response.read()
            self.assertEqual(self.total(token), 1)
        for state in self.states:
            with state.lock:
                self.assertGreaterEqual(state.health_checks, 2)
                self.assertTrue(state.calls)
                self.assertTrue(state.handshakes)
                self.assertEqual(set(state.handshakes), {plaintext.ORIGIN_HOST})
                self.assertEqual(state.wrong_hosts, 0)

    def rejected_identity(self, variable, value, expected_sni):
        self.close_caddy()
        # Fresh transport/health state, same independently running TLS origins.
        with_counts = []
        for state in self.states:
            with state.lock:
                with_counts.append((state.health_checks, len(state.handshakes)))
                self.assertFalse(state.calls)
        env = self.proxy_env.copy()
        env[variable] = value
        self.start_caddy(env)
        plaintext.eventually(lambda: all(len(s.handshakes) >= n + 3
                                        for s, (_, n) in zip(self.states, with_counts)))
        # Three actual TLS attempts per origin exceed health_fails=2. A failed
        # handshake must not deliver even the public health GET, let alone POST.
        for _ in range(4):
            token, _, response = self.request()
            self.assertEqual(response.status, 503)
            response.read()
            self.assertEqual(self.total(token), 0)
        for state, (health, handshakes) in zip(self.states, with_counts):
            with state.lock:
                self.assertEqual(state.health_checks, health)
                self.assertFalse(state.calls)
                self.assertEqual(set(state.handshakes[handshakes:]), {expected_sni})
        self.close_caddy()
        self.start_caddy(self.proxy_env)
        plaintext.eventually(lambda: all(s.health_checks >= n + 2
                                        for s, (n, _) in zip(self.states, with_counts)))
        nodes = set()
        for _ in range(4):
            token, _, response = self.request()
            self.assertEqual(response.status, 200)
            nodes.add(json.loads(response.read())["node"])
            self.assertEqual(self.total(token), 1)
        self.assertEqual(nodes, {"a", "b"})

    def test_untrusted_ca_rejects_probe_and_business(self):
        self.rejected_identity("SUB2API_LB_TRUSTED_CA_FILE", str(self.wrong_ca),
                               plaintext.ORIGIN_HOST)

    def test_wrong_sni_rejects_probe_and_business(self):
        self.rejected_identity("SUB2API_LB_TLS_SERVER_NAME", "wrong.fixture.invalid",
                               "wrong.fixture.invalid")


if __name__ == "__main__":
    unittest.main(verbosity=2)
