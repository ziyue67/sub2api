import base64
import json
import os
import subprocess
import tempfile
import time
import unittest
from pathlib import Path
from unittest.mock import patch

from tools import openai_excel_oauth_adapter as adapter


def token(payload):
    b64 = base64.urlsafe_b64encode(json.dumps(payload).encode()).decode().rstrip('=')
    return 'e30.' + b64 + '.test'


class ExcelAdapterTests(unittest.TestCase):
    def credentials(self):
        return {'client_id': adapter.CLIENT_ID, 'chatgpt_account_id': 'workspace-a',
                'refresh_token': 'synthetic-refresh',
                'access_token': token({'client_id': adapter.CLIENT_ID, 'exp': int(time.time())+3600,
                                      'https://api.openai.com/auth': {'chatgpt_account_id': 'workspace-a'}}),
                'id_token': token({'aud': adapter.CLIENT_ID, 'email': 'test@example.invalid'})}

    def test_matching_excel_session_accepted(self):
        adapter.validate_excel_credentials(self.credentials(), 'test@example.invalid', 'workspace-a')

    def test_wrong_client_email_and_workspace_rejected(self):
        for changes,email,workspace in [({'client_id':'codex'},'test@example.invalid','workspace-a'),
                                        ({},'someone@example.invalid','workspace-a'),
                                        ({},'test@example.invalid','workspace-b'),
                                        ({'id_token':token({'aud':'codex','email':'test@example.invalid'})},'test@example.invalid','workspace-a'),
                                        ({'refresh_token':''},'test@example.invalid','workspace-a')]:
            with self.subTest(changes=changes),self.assertRaises(ValueError):
                adapter.validate_excel_credentials({**self.credentials(),**changes},email,workspace)

    def test_unknown_runtime_fails_before_creating_or_modifying_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory)/'source';(root/'src').mkdir(parents=True)
            script=root/'src/protocol-login.mjs';script.write_text('// changed runtime')
            output=Path(directory)/'temp';output.mkdir()
            with self.assertRaises(ValueError):adapter.prepare_excel_runtime(root,output)
            self.assertEqual(script.read_text(),'// changed runtime')
            self.assertEqual(list(output.iterdir()),[])

    def test_staging_keeps_shared_runtime_unchanged(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory)/'source';(root/'src').mkdir(parents=True);(root/'node_modules').mkdir()
            script=root/'src/protocol-login.mjs';script.write_text('original')
            output=Path(directory)/'temp';output.mkdir()
            with patch.object(adapter,'excel_source',return_value='excel'):
                staged=adapter.prepare_excel_runtime(root,output)
            self.assertEqual(staged.read_text(),'excel');self.assertEqual(script.read_text(),'original')
            self.assertEqual((staged.parent.parent/'node_modules').resolve(),root/'node_modules')

    def test_contract_change_never_silently_partially_patches(self):
        with self.assertRaises(ValueError):adapter.replace_exact('old old','old','new')
        self.assertEqual(adapter.replace_exact('old old','old','new',count=2),'new new')

    @unittest.skipUnless(os.getenv('EXCEL_TEST_TOSUB2_ROOT'), 'set EXCEL_TEST_TOSUB2_ROOT to the pinned dependency')
    def test_pinned_dependency_transforms_and_parses_without_mutating_original(self):
        root=Path(os.environ['EXCEL_TEST_TOSUB2_ROOT'])
        original=(root/'src/protocol-login.mjs').read_bytes()
        with tempfile.TemporaryDirectory() as d:
            staged=adapter.prepare_excel_runtime(root,Path(d))
            subprocess.run(['node','--check',str(staged)],check=True,capture_output=True)
        self.assertEqual((root/'src/protocol-login.mjs').read_bytes(),original)


if __name__=='__main__':unittest.main()
