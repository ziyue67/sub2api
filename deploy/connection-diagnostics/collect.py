#!/usr/bin/env python3
"""Bounded, retryable edge metadata collection; never sends a model request."""
import argparse
import datetime as dt
import fcntl
import hashlib
import ipaddress
import json
import math
import os
from pathlib import Path
import re
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request
import uuid

MAX_LINE = 64 * 1024
MAX_BYTES = 4 * 1024 * 1024


def safe_path(raw):
    path = str(raw or '/[redacted]').split('?', 1)[0].split('#', 1)[0]
    allowed = set('v1 v1beta openai antigravity backend-api codex api models messages responses compact chat completions embeddings images generations edits variations videos audio transcriptions translations speech live realtime health readyz usage sub2api billing admin connections edge'.split())
    path = '/'.join(part if not part or part in allowed else '[redacted]' for part in path.split('/'))
    return path if path.startswith('/') and len(path.encode()) <= 128 else '/[redacted]'



def normalize(line, node):
    """Allowlist fields; no raw line, query, header, body or error message leaves host."""
    v = json.loads(line)
    # Caddy's standard-library TLS logger has no HTTP URL. Export only the
    # parsed peer and fixed error kind; never export the raw handshake message.
    if v.get('logger') == 'http.stdlib' and 'TLS handshake error from ' in v.get('msg', ''):
        match = re.search(r'TLS handshake error from (\[[0-9a-fA-F:]+\]|[0-9.]+):[0-9]+:', v['msg'])
        if not match:
            raise ValueError('TLS peer unavailable')
        peer = str(ipaddress.ip_address(match.group(1).strip('[]')))
        timestamp = dt.datetime.fromtimestamp(float(v['ts']), dt.timezone.utc)
        identity = str(uuid.uuid5(uuid.NAMESPACE_URL, node + ':' + hashlib.sha256(line).hexdigest()))
        return dict(id=identity, source='caddy', time=timestamp.isoformat(), request_id='', host='', scheme='https', method='', path='', client_ip=peer, http_version='', tls_version='', status=0, duration_ms=0, bytes=0, upstream_status='', network_error='tls_error')
    source = v.get('source', 'caddy')
    if source not in ('nginx', 'caddy'):
        raise ValueError('source')
    r = v.get('request', {}) if source == 'caddy' else v
    host = str(r.get('host', ''))
    if not re.fullmatch(r'[a-zA-Z0-9.\[\]:_-]{1,255}', host):
        raise ValueError('host')
    seconds = float(v.get('duration', 0))
    stamp = float(v['ts'])
    if not math.isfinite(seconds) or not 0 <= seconds <= 86400 or not math.isfinite(stamp):
        raise ValueError('time')
    # Access log timestamps are end times; display start with the observed duration.
    timestamp = dt.datetime.fromtimestamp(stamp - seconds, dt.timezone.utc)
    ip = str(ipaddress.ip_address(r.get('client_ip') or r.get('remote_ip', '')))
    status = int(v.get('status', 0))
    kind = 'client_closed' if status == 499 else 'timeout' if status in (408, 504) else 'upstream_unavailable' if status == 502 else ''
    if source == 'nginx' and not v.get('completion') and 0 < status < 400:
        kind = 'client_closed'
    tls = r.get('tls', '')
    if isinstance(tls, dict):
        tls = {771: 'TLSv1.2', 772: 'TLSv1.3'}.get(tls.get('version'), 'TLS')
    scheme = str(r.get('scheme') or ('https' if tls else 'http'))
    # Deterministic IDs make a retry after failed/lost HTTP ack idempotent.
    event_id = str(uuid.uuid5(uuid.NAMESPACE_URL, node + ':' + hashlib.sha256(line).hexdigest()))
    request_id = str(v.get('request_id') or v.get('edge_id') or '')
    if len(request_id) > 128 or (request_id and not re.fullmatch(r'[A-Za-z0-9_.:-]+', request_id)):
        request_id = ''
    return dict(id=event_id, source=source, time=timestamp.isoformat(), request_id=request_id,
                host=host, scheme=scheme, method=str(r.get('method', ''))[:16],
                path=safe_path(v.get('public_path', r.get('path', ''))), client_ip=ip,
                http_version=str(r.get('proto', ''))[:16], tls_version=str(tls)[:32],
                status=status, duration_ms=seconds * 1000,
                bytes=int(v.get('bytes', v.get('size', 0))),
                upstream_status=str(v.get('upstream_status', ''))[:64], network_error=kind)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError('collector endpoint redirected')


def send(events, endpoint, key):
    url = urllib.parse.urlsplit(endpoint)
    if url.scheme != 'https' and not (url.scheme == 'http' and url.hostname in ('127.0.0.1', '::1', 'localhost')):
        raise ValueError('HTTPS or loopback endpoint required')
    if url.username or url.password or url.query or url.fragment:
        raise ValueError('invalid collector endpoint')
    payload = json.dumps({'events': events}, allow_nan=False).encode()
    req = urllib.request.Request(endpoint, payload, headers={'Content-Type': 'application/json', 'x-api-key': key})
    with urllib.request.build_opener(NoRedirect).open(req, timeout=10) as response:
        result = json.load(response)
        if result.get('code') != 0 or result.get('data', {}).get('accepted') != len(events):
            raise ValueError('collector acknowledgement mismatch')


def save_state(path, state):
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd, name = tempfile.mkstemp(dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as out:
            json.dump(state, out)
            out.flush()
            os.fsync(out.fileno())
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def collect(path, state_path, node, endpoint, key):
    """One bounded pass. Checkpoint only after all valid records are acknowledged."""
    state = json.loads(state_path.read_text()) if state_path.exists() else {}
    current = path.stat()
    selected = path
    old_inode = state.get('inode')
    if old_inode and old_inode != current.st_ino:
        candidates = [p for p in path.parent.glob(path.name + '*') if p.is_file() and p.stat().st_ino == old_inode]
        if not candidates:
            raise ValueError('unread rotated file missing; manual checkpoint recovery required')
        selected = candidates[0]
    position = int(state.get('offset', 0))
    if position > selected.stat().st_size:
        raise ValueError('log truncated; manual checkpoint recovery required')
    batch = []
    skipped = 0
    with selected.open('rb') as stream:
        stream.seek(position)
        read = 0
        while read < MAX_BYTES:
            start = stream.tell()
            line = stream.readline(MAX_LINE + 1)
            if not line:
                break
            if len(line) > MAX_LINE:
                raise ValueError('oversized log line; checkpoint unchanged')
            if not line.endswith(b'\n'):
                stream.seek(start)
                break
            read += len(line)
            try:
                batch.append(normalize(line, node))
            except (ValueError, KeyError, TypeError, OverflowError):
                skipped += 1
            if len(batch) == 50:
                send(batch, endpoint, key)
                batch = []
        position = stream.tell()
        at_eof = stream.read(1) == b''
    if batch:
        send(batch, endpoint, key)
    inode = selected.stat().st_ino
    if selected != path and at_eof:
        inode, position = current.st_ino, 0
    save_state(state_path, {'inode': inode, 'offset': position})
    if skipped:
        print(f'connection collector: {skipped} malformed lines skipped', file=sys.stderr)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--log', type=Path, required=True)
    parser.add_argument('--state', type=Path, required=True)
    parser.add_argument('--node', required=True)
    args = parser.parse_args()
    args.state.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    with open(str(args.state) + '.lock', 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        collect(args.log, args.state, args.node, os.environ['SUB2API_CONNECTION_ENDPOINT'], os.environ['SUB2API_CONNECTION_ADMIN_KEY'])


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, KeyError, urllib.error.URLError):
        # Do not print exception text: endpoint URLs and server bodies may be sensitive.
        print('connection collector failed; checkpoint retained for retry', file=sys.stderr)
        sys.exit(1)
