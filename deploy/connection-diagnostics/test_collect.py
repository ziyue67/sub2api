import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('collector', Path(__file__).with_name('collect.py'))
collector = importlib.util.module_from_spec(spec)
spec.loader.exec_module(collector)


def nginx_line(**updates):
    event = dict(source='nginx', ts='1792566000.5', duration='1.25', host='relay.example.com:443', scheme='https', method='POST', path='/v1/responses?api_key=secret', client_ip='192.0.2.10', proto='HTTP/2.0', tls='TLSv1.3', status='502', bytes='50', request_id='server-id', upstream_status='502', completion='OK')
    event.update(updates)
    return json.dumps(event).encode() + b'\n'


class CollectorTests(unittest.TestCase):
    def test_allowlist_and_stable_id(self):
        line = nginx_line(headers={'Authorization': 'private'}, body='private')
        event = collector.normalize(line, 'edge-1')
        self.assertEqual(event['path'], '/v1/responses')
        self.assertEqual(event['host'], 'relay.example.com:443')
        self.assertEqual(event['network_error'], 'upstream_unavailable')
        self.assertEqual(event, collector.normalize(line, 'edge-1'))
        self.assertNotIn('private', json.dumps(event))
        self.assertNotIn('secret', json.dumps(event))

    def test_tls_failure_does_not_invent_url_or_user(self):
        line = json.dumps(dict(logger='http.stdlib', ts=1792566000.5, msg='http: TLS handshake error from [2001:db8::1]:1234: tls: private detail')).encode()
        event = collector.normalize(line, 'edge-1')
        self.assertEqual(event['network_error'], 'tls_error')
        self.assertEqual(event['client_ip'], '2001:db8::1')
        self.assertEqual(event['host'], '')
        self.assertEqual(event['path'], '')
        self.assertNotIn('private', json.dumps(event))

    def test_failed_ack_keeps_checkpoint_and_retries_same_event(self):
        with tempfile.TemporaryDirectory() as directory:
            log, state = Path(directory) / 'access.jsonl', Path(directory) / 'state.json'
            log.write_bytes(nginx_line())
            events = []
            def fail(batch, *_):
                events.extend(batch)
                raise ValueError('ack failed')
            with patch.object(collector, 'send', side_effect=fail):
                with self.assertRaises(ValueError):
                    collector.collect(log, state, 'edge', 'http://127.0.0.1/', 'private')
            self.assertFalse(state.exists())
            with patch.object(collector, 'send') as send:
                collector.collect(log, state, 'edge', 'http://127.0.0.1/', 'private')
                self.assertEqual(send.call_args.args[0][0]['id'], events[0]['id'])
            self.assertEqual(json.loads(state.read_text())['offset'], log.stat().st_size)

    def test_rename_rotation_drains_old_file_first(self):
        with tempfile.TemporaryDirectory() as directory:
            log, state = Path(directory) / 'access.jsonl', Path(directory) / 'state.json'
            log.write_bytes(nginx_line())
            with patch.object(collector, 'send'):
                collector.collect(log, state, 'edge', '', '')
                with log.open('ab') as stream:
                    stream.write(nginx_line(request_id='old-file-late'))
                log.rename(Path(directory) / 'access.jsonl.1')
                log.write_bytes(nginx_line(request_id='new-file'))
                with patch.object(collector, 'send') as send:
                    collector.collect(log, state, 'edge', '', '')
                    self.assertEqual(send.call_args.args[0][0]['request_id'], 'old-file-late')
                    collector.collect(log, state, 'edge', '', '')
                    self.assertEqual(send.call_args.args[0][0]['request_id'], 'new-file')

    def test_no_checkpoint_past_partial_line(self):
        with tempfile.TemporaryDirectory() as directory:
            log, state = Path(directory) / 'access.jsonl', Path(directory) / 'state.json'
            log.write_bytes(nginx_line()[:-1])
            with patch.object(collector, 'send') as send:
                collector.collect(log, state, 'edge', '', '')
                send.assert_not_called()
            self.assertEqual(json.loads(state.read_text())['offset'], 0)


if __name__ == '__main__':
    unittest.main()
