import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path
from urllib.parse import urlencode

from tools.openai_siwc_oauth_adapter import prepare_siwc_runtime, siwc_source


class SIWCAdapterTests(unittest.TestCase):
    def test_changed_dependency_is_rejected_without_modifying_source(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / 'source'
            (root / 'src').mkdir(parents=True)
            original = root / 'src/protocol-login.mjs'
            original.write_text('// unrecognized dependency')
            target = Path(directory) / 'target'
            target.mkdir()
            with self.assertRaises(ValueError):
                prepare_siwc_runtime(root, target)
            self.assertEqual(original.read_text(), '// unrecognized dependency')
            self.assertFalse((target / 'siwc-runtime').exists())

    @unittest.skipUnless(os.getenv('EXCEL_TEST_TOSUB2_ROOT'), 'pinned runtime is provided by packaging CI')
    def test_pinned_runtime_exports_callback_without_exchanging_tokens(self):
        root = Path(os.environ['EXCEL_TEST_TOSUB2_ROOT'])
        with tempfile.TemporaryDirectory() as directory:
            script = prepare_siwc_runtime(root, Path(directory))
            source = script.read_text()
            self.assertIn('siwc_callback_url:codex.callbackUrl', source)
            self.assertIn('if (client.siwcOAuth) return client.siwcOAuth', source)
            self.assertNotIn('console.log(codex.callbackUrl)', source)
            self.assertTrue((script.parent / 'openai_siwc_password_flow.mjs').is_file())
            self.assertNotEqual(source, (root / 'src/protocol-login.mjs').read_text())
            with self.assertRaises(ValueError):
                siwc_source(source)

    @unittest.skipUnless(os.getenv('EXCEL_TEST_TOSUB2_ROOT'), 'pinned runtime is provided by packaging CI')
    def test_pinned_runner_password_totp_workspace_callback_and_consent(self):
        root = Path(os.environ['EXCEL_TEST_TOSUB2_ROOT'])
        auth = 'https://auth.openai.com/api/accounts/authorize?' + urlencode({
            'state': 'synthetic-state', 'nonce': 'synthetic-nonce',
            'redirect_uri': 'http://127.0.0.1:1455/auth/callback',
            'scope': 'openid chatgpt.tokens.use.direct',
        })
        for mode, marker in (('success', None), ('consent', 'SIWC_INTERACTION_REQUIRED'),
                             ('wrong-state', 'SIWC_CALLBACK_INVALID'), ('password-rejected', 'Password was rejected')):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory:
                temporary = Path(directory)
                script = prepare_siwc_runtime(root, temporary)
                shutil.copy2(Path(__file__).parent / 'fixtures/siwc-http-transport.mjs', script.parent / 'tls-transport.mjs')
                output = temporary / 'callback.json'
                routes = temporary / 'routes.txt'
                env = dict(os.environ, OPENAI_SIWC_AUTH_URL=auth,
                           OPENAI_SIWC_EXPECTED_WORKSPACE='expected-workspace',
                           CHATGPT_LOGIN_PASSWORD='synthetic-password', CHATGPT_TOTP_SECRET='JBSWY3DPEHPK3PXP',
                           SIWC_TEST_MODE=mode, SIWC_TEST_ROUTES=str(routes))
                result = subprocess.run([os.getenv('NODE_EXECUTABLE', 'node'), str(script),
                                         '--email', 'test@example.test', '--output-mode', 'sub2api',
                                         '--sub2api-out', str(output)], cwd=directory, env=env,
                                        capture_output=True, text=True, timeout=20)
                private = result.stdout + result.stderr
                if marker is None:
                    self.assertEqual(result.returncode, 0, private)
                    payload = json.loads(output.read_text())
                    self.assertEqual(set(payload), {'siwc_callback_url'})
                    self.assertIn('code=synthetic-code', payload['siwc_callback_url'])
                    self.assertNotIn('synthetic-code', private)
                else:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn(marker, private)
                    self.assertFalse(output.exists())
                trace = routes.read_text()
                self.assertNotIn('/oauth/token', trace)
                self.assertNotIn('chatgpt.com', trace)
                self.assertEqual(trace.count('POST /api/accounts/password/verify'), 1)
                if mode != 'password-rejected':
                    self.assertIn('POST /api/accounts/mfa/verify', trace)
                    self.assertIn('POST /api/accounts/workspace/select', trace)
