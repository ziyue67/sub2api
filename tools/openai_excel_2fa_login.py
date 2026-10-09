#!/usr/bin/env python3
"""Isolated Excel login using the existing password/TOTP worker; no queue/DB writes."""
import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
from types import SimpleNamespace

try:
    from . import openai_oauth_reauth_worker as worker
except ImportError:
    try:
        import openai_oauth_reauth_worker as worker
    except ModuleNotFoundError:
        import worker  # Relocatable runtime names the entrypoint worker.py.


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--input-file',required=True)
    parser.add_argument('--tosub2-root',required=True)
    parser.add_argument('--output-dir',required=True)
    args=parser.parse_args()
    os.umask(0o077)
    path=Path(args.input_file)
    if path.is_symlink() or path.stat().st_mode & 0o077:
        raise worker.WorkerError('input file must be private and not a symlink')
    claim=json.loads(path.read_text())
    claim.update(task_id=1,oauth_profile='excel',engine='local_worker',credential_mode='password_totp')
    output=Path(args.output_dir);output.mkdir(mode=0o700,parents=True,exist_ok=False)
    def write(name,value):
        with (output/name).open('x') as f:json.dump(value,f,indent=2)
    class IsolatedAPI:
        config=SimpleNamespace(tosub2_root=Path(args.tosub2_root))
        def progress(self,task_id,stage):
            print(json.dumps({'stage':stage,'time':datetime.now(timezone.utc).isoformat()}),flush=True)
        def credentials(self,task_id,credentials,extra):
            write('credentials.json',credentials)
            write('account.json',{'platform':'openai','type':'oauth','credentials':credentials,'extra':extra})
            return {'status':'succeeded'}
        def private_protocol_diagnostics(self,stdout,stderr):
            write('private-protocol-output.json',{'stdout':stdout,'stderr':stderr})
    try:
        worker.process_password_claim(IsolatedAPI(),claim)
        result={'status':'succeeded','oauth_profile':'excel','account_id':claim['account_id'],
                'credentials_saved':True,'production_account_updated':False}
    except Exception as exc:
        result={'status':'failed','error_class':type(exc).__name__,
                'error':str(exc) if isinstance(exc,worker.WorkerError) else 'isolated_login_failed',
                'production_account_updated':False}
    write('result.json',result)
    print(json.dumps(result),flush=True)
    return 0 if result['status']=='succeeded' else 1


if __name__=='__main__':
    try:raise SystemExit(main())
    except Exception as exc:
        print(json.dumps({'status':'failed','error_class':type(exc).__name__}),flush=True)
        raise SystemExit(1)
