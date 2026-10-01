import io
import json
import os
import sys
import tempfile
import time
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock, patch
from urllib.error import HTTPError

from tools import openai_oauth_reauth_worker as worker


class ChunkResponse:
    def __init__(self, chunks):
        self.chunks = iter(chunks)
        self.closed = False
    def read1(self, size):
        return next(self.chunks, b"")
    def __enter__(self):
        return self
    def __exit__(self, *_args):
        self.closed = True


def parallel_probe(config, stop=None):
    root = Path(config.base_url)
    (root / config.worker_id).touch()
    deadline = time.monotonic() + 3
    while len(list(root.glob('probe-slot-*'))) < 3 and time.monotonic() < deadline:
        time.sleep(.01)
    if len(list(root.glob('probe-slot-*'))) == 3:
        (root / ('done-' + config.worker_id)).touch()
    time.sleep(.4)


class ReauthEngineTests(unittest.TestCase):
    def claim(self):
        return dict(task_id=7, account_id=42, credential_mode='password_totp',
                    engine='session_studio', login_email='demo@example.com',
                    password='synthetic-password', totp_secret='synthetic-totp',
                    relogin_endpoint='https://session.example/api/v1/relogin',
                    relogin_headers={'X-Session-Studio-Relogin':'1','X-Session-Studio-Client':'{{uuid}}'})
    def credentials(self):
        return dict(access_token='synthetic-access',refresh_token='synthetic-refresh',id_token='synthetic-id')
    def api(self):
        return SimpleNamespace(progress=Mock(), credentials=Mock(return_value={'status':'succeeded'}))
    def test_stream_stops_on_terminal_result_without_eof(self):
        # A socket would stay open for later heartbeats. Reading after this
        # terminal result is an error, rather than an artificial EOF.
        response = Mock()
        raw=json.dumps({'type':'result','payload':{'credential':self.credentials()}}).encode()
        response.read1.side_effect=[b'{"type":"heartbeat"}\n',raw[:20],raw[20:],AssertionError('waited for EOF')]
        self.assertEqual(worker._session_studio_result(response)['credential'],self.credentials())
        self.assertEqual(response.read1.call_count,3)
    def test_plain_json_and_utf8_split(self):
        raw=json.dumps({'credential':self.credentials(),'label':'中文'},ensure_ascii=False).encode()
        response=ChunkResponse([bytes([c]) for c in raw])
        self.assertEqual(worker._session_studio_result(response)['label'],'中文')
    def test_invalid_and_unbounded_results_fail_closed(self):
        for data in (b'[]',b'{bad',b'{"type":"result","payload":null}',b'{"type":"heartbeat"}'):
            with self.subTest(data=data),self.assertRaises(worker.WorkerError):
                worker._session_studio_result(ChunkResponse([data]))
        with patch.object(worker,'MAX_RESPONSE_BYTES',8),self.assertRaisesRegex(worker.WorkerError,'too large'):
            worker._session_studio_result(ChunkResponse([b' '*65]))
    def test_session_engine_request_and_cas_submission(self):
        api=self.api();response=ChunkResponse([json.dumps({'credential':self.credentials()}).encode()])
        with patch.dict(os.environ,{'OPENAI_REAUTH_WORKER_TOKEN':'never-forward'}),patch.object(worker.NO_REDIRECT_OPENER,'open',return_value=response) as opener,patch.object(worker.subprocess,'run') as local:
            worker.process_password_claim(api,self.claim())
        req=opener.call_args.args[0]
        self.assertEqual(json.loads(req.data),dict(action='start',auth_mode='password_2fa',email='demo@example.com',password='synthetic-password',mfa_secret='synthetic-totp'))
        self.assertNotIn('never-forward',str(req.headers))
        self.assertNotIn('{{uuid}}',str(req.headers))
        self.assertEqual(req.get_header('Origin'),'https://session.example')
        api.credentials.assert_called_once_with(7,self.credentials(),{})
        local.assert_not_called();self.assertTrue(response.closed)
        self.assertEqual([c.args[1] for c in api.progress.call_args_list],['starting','protocol_connecting','applying_credentials'])
    def test_missing_endpoint_or_invalid_engine_never_falls_back(self):
        for overrides in ({'relogin_endpoint':''},{'relogin_endpoint':'http://session.example/'},{'relogin_endpoint':'https://user:pass@session.example/'},{'relogin_endpoint':'https://session.example/?token=secret'},{'engine':'typo'},{'proxy_url':'http://private-proxy/'},{'relogin_headers':{'X-OpenAI-Reauth-Worker-Token':'secret'}}):
            with self.subTest(overrides=overrides),patch.object(worker.NO_REDIRECT_OPENER,'open') as opener,patch.object(worker.subprocess,'run') as local:
                with self.assertRaises(worker.WorkerError):
                    worker.process_password_claim(self.api(),dict(self.claim(),**overrides))
                opener.assert_not_called();local.assert_not_called()
    def test_provider_failures_do_not_expose_secrets_or_switch_engine(self):
        for response in ({'error':{'code':'synthetic-password','message':'synthetic-totp'}},{'credential':{'access_token':'synthetic-access'}}):
            api=self.api()
            with patch.object(worker.NO_REDIRECT_OPENER,'open',return_value=ChunkResponse([json.dumps(response).encode()])),patch.object(worker.subprocess,'run') as local:
                with self.assertRaises(worker.WorkerError) as failure: worker.process_password_claim(api,self.claim())
                self.assertNotIn('synthetic',str(failure.exception));local.assert_not_called();api.credentials.assert_not_called()
    def test_redirect_response_not_followed(self):
        with patch.object(worker.NO_REDIRECT_OPENER,'open',side_effect=HTTPError('https://session.example/',302,'secret',{},io.BytesIO(b'secret'))):
            with self.assertRaisesRegex(worker.WorkerError,'^Session Studio HTTP 302$'):
                worker.process_password_claim(self.api(),self.claim())
        handler=worker._NoRedirect()
        self.assertIsNone(handler.redirect_request(None,None,302,'',{},'https://other.example/'))
    def test_rejected_cas_is_not_reported_successful(self):
        api=self.api();api.credentials.return_value={'status':'failed','error':'synthetic-secret'}
        with patch.object(worker.NO_REDIRECT_OPENER,'open',return_value=ChunkResponse([json.dumps({'credential':self.credentials()}).encode()])):
            with self.assertRaisesRegex(worker.WorkerError,'credentials were not accepted'):
                worker.process_password_claim(api,self.claim())
    def test_config_concurrency_bounds(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);(root/'src').mkdir()
            for name in ('protocol-login.mjs','tls-transport.mjs'):(root/'src'/name).touch()
            base=dict(OPENAI_REAUTH_WORKER_TOKEN='x'*32,TOSUB2_ROOT=directory)
            for val in ('0','17','-1','2.5','NaN'):
                with patch.dict(os.environ,dict(base,OPENAI_REAUTH_CONCURRENCY=val),clear=True),self.assertRaises(worker.WorkerError):worker.WorkerConfig.from_env()
            for val in ('1','3','16'):
                with patch.dict(os.environ,dict(base,OPENAI_REAUTH_CONCURRENCY=val),clear=True):self.assertEqual(worker.WorkerConfig.from_env().concurrency,int(val))
    def test_slot_ids_are_distinct_and_bounded(self):
        cfg=worker.WorkerConfig('http://127.0.0.1','x'*32,'x'*128,None)
        ids=[worker.worker_slot_config(cfg,i).worker_id for i in range(1,17)]
        self.assertEqual(len(set(ids)),16);self.assertTrue(all(len(i)<=128 for i in ids))
    @unittest.skipUnless(sys.platform.startswith('linux'),'fork integration')
    def test_three_real_worker_processes_overlap(self):
        with tempfile.TemporaryDirectory() as directory:
            cfg=worker.WorkerConfig(directory,'x'*32,'probe',None,concurrency=3)
            with patch.object(worker.WorkerAPI,'runtime_concurrency',return_value=3),patch.object(worker,'_worker_loop',parallel_probe),self.assertRaisesRegex(worker.WorkerError,'process exited'):
                worker.run_worker_pool(cfg)
            self.assertEqual(len(list(Path(directory).glob('done-*'))),3)
    def test_once_does_not_start_a_pool(self):
        cfg=worker.WorkerConfig('http://127.0.0.1','x'*32,'test',None,concurrency=3)
        with patch('sys.argv',['worker','--once']),patch.object(worker.WorkerConfig,'from_env',return_value=cfg),patch.object(worker.shutil,'which',return_value='node'),patch.object(worker,'run_once',return_value=False) as run,patch.object(worker,'run_worker_pool') as pool:
            self.assertEqual(worker.main(),0);run.assert_called_once();pool.assert_not_called()


if __name__=='__main__':unittest.main()
