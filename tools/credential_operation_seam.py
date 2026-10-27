"""Generate offline worker results for the remote storage/API/UI seam test.

All endpoints are mocked. This script never performs login or network requests.
"""
import json
import sys
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock, patch

from tools import openai_oauth_reauth_worker as worker


def generate():
    cases = json.loads(Path('tools/fixtures/credential-operation-errors.json').read_text())
    results = []
    for case in cases:
        payload = {**case['payload'], 'status': 'failed'}
        # Reuse the worker's terminal decoder and failure reporter with an
        # in-memory response; no real credential or external endpoint is used.
        raw = json.dumps(payload).encode()
        chunks = iter((raw, b''))
        class Response:
            def read1(self, _size): return next(chunks, b'')
            def __enter__(self): return self
            def __exit__(self, *_args): pass
        api = SimpleNamespace(config=SimpleNamespace(), progress=Mock(), credentials=Mock(),
                              claim=Mock(return_value=dict(task_id=7, account_id=42,
                              credential_mode='password_totp', engine='session_studio', oauth_profile='codex',
                              login_email='fixture', password='synthetic-private',
                              relogin_endpoint='https://fixture.invalid/relogin', relogin_headers={})))
        reporter = worker.WorkerAPI(SimpleNamespace(worker_id='fixture-worker'))
        reporter._post = Mock()
        api.fail = reporter.fail
        with patch.object(worker.NO_REDIRECT_OPENER, 'open', return_value=Response()):
            assert worker.run_once(api, None)
        api.credentials.assert_not_called()
        reason = reporter._post.call_args.args[1]['reason']
        assert 'synthetic-private' not in reason
        expected = case['code'] if case['code'] != 'unknown' else 'provider_failed'
        assert f'reason={expected} ' in reason, (case['name'], reason)
        results.append({**case, 'worker_reason': reason})
    return results


if __name__ == '__main__':
    Path(sys.argv[1]).write_text(json.dumps(generate(), indent=2) + '\n')
