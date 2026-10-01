import copy
import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('upgrade', Path(__file__).parents[1] / 'upgrade-k3s.py')
u = importlib.util.module_from_spec(spec)
spec.loader.exec_module(u)


def workload():
    image = u.IMAGE + '@sha256:' + 'a' * 64
    return {'metadata': {'resourceVersion': '7'}, 'spec': {
        'template': {'metadata': {'labels': {'app': 'test'}}, 'spec': {
            'containers': [{'name': 'sub2api', 'image': image, 'env': [{'name': 'RUNTIME_ROLE', 'value': 'gateway'}]}],
            'initContainers': [{'name': 'verify-runtime', 'image': image, 'command': ['/bin/sh', '-ec',
                'echo "' + 'b' * 64 + '  /app/sub2api" | sha256sum -c -']}],
            'volumes': [{'name': 'data', 'persistentVolumeClaim': {'claimName': 'keep'}}]}}}}


class UpgradeTests(unittest.TestCase):
    def test_updates_image_and_init_checksum_without_changing_configuration(self):
        before = workload()
        saved = copy.deepcopy(before)
        image = u.IMAGE + '@sha256:' + 'c' * 64
        result = u.replacement(before, image, 'd' * 64, 'sub2api')
        self.assertEqual(before, saved)
        self.assertEqual(result['spec']['containers'][0]['image'], image)
        self.assertEqual(result['spec']['initContainers'][0]['image'], image)
        self.assertIn('d' * 64, result['spec']['initContainers'][0]['command'][2])
        self.assertEqual(result['spec']['volumes'], saved['spec']['template']['spec']['volumes'])
        self.assertEqual(result['spec']['containers'][0]['env'], saved['spec']['template']['spec']['containers'][0]['env'])

    def test_same_release_is_idempotent(self):
        before = workload()
        image = before['spec']['template']['spec']['containers'][0]['image']
        self.assertEqual(u.replacement(before, image, 'b' * 64, 'sub2api'), before['spec']['template'])

    def test_init_args_checksum_supported(self):
        before = workload()
        init = before['spec']['template']['spec']['initContainers'][0]
        init['args'] = [init['command'].pop()]
        result = u.replacement(before, u.IMAGE + '@sha256:' + 'c' * 64, 'd' * 64, 'sub2api')
        self.assertIn('d' * 64, result['spec']['initContainers'][0]['args'][0])

    def test_missing_verification_checksum_rejected(self):
        before = workload()
        before['spec']['template']['spec']['initContainers'][0]['command'] = ['true']
        with self.assertRaises(ValueError):
            u.replacement(before, 'new', 'd' * 64, 'sub2api')

    def test_different_application_init_image_rejected(self):
        before = workload()
        before['spec']['template']['spec']['initContainers'][0]['image'] = u.IMAGE + ':old'
        with self.assertRaises(ValueError):
            u.replacement(before, 'new', 'd' * 64, 'sub2api')

    def test_rollback_uses_current_resource_version(self):
        before = workload()
        old = before['spec']['template']
        desired = u.replacement(before, 'new', 'd' * 64, 'sub2api')
        current = copy.deepcopy(before)
        current['metadata']['resourceVersion'] = '99'
        current['spec']['template'] = desired
        ops = u.guarded_patch(current, desired, old)
        self.assertEqual(ops[0]['value'], '99')
        self.assertEqual(ops[1]['value'], desired)
        self.assertEqual(ops[2]['value'], old)

    def test_concurrent_template_change_blocks_rollback(self):
        before = workload()
        with self.assertRaises(RuntimeError):
            u.guarded_patch(before, {'somebody': 'else'}, before['spec']['template'])

    def test_annotated_tag_resolves_to_commit(self):
        responses = [
            {'tag_name': 'v2.9.6', 'draft': False, 'prerelease': False},
            {'object': {'type': 'tag', 'sha': 'a' * 40}},
            {'object': {'type': 'commit', 'sha': 'b' * 40}}]
        with patch.object(u, 'api', side_effect=responses):
            self.assertEqual(u.resolve_release('v2.9.6'), ('v2.9.6', 'b' * 40))

    def test_unpublished_release_rejected(self):
        for key in ('draft', 'prerelease'):
            release = {'tag_name': 'v2.9.6', 'draft': False, 'prerelease': False, key: True}
            with patch.object(u, 'api', return_value=release), self.assertRaises(ValueError):
                u.resolve_release('latest')

    def test_version_matches_exact_commit(self):
        u.verify_version('Sub2API 2.9.6 (commit: ' + 'a' * 40 + ', built: now)', 'v2.9.6', 'a' * 40)
        with self.assertRaises(ValueError):
            u.verify_version('Sub2API 2.9.6 (commit: ' + 'b' * 40 + ', built: now)', 'v2.9.6', 'a' * 40)

    def test_registry_image_revision_mismatch_rejected(self):
        config = json.dumps({'os': 'linux', 'architecture': 'amd64', 'config': {'Labels': {
            'org.opencontainers.image.version': '2.9.6', 'org.opencontainers.image.revision': 'b' * 40}}}).encode()
        digest = 'sha256:' + u.hashlib.sha256(config).hexdigest()
        manifest = json.dumps({'config': {'digest': digest}}).encode()
        with patch.object(u, 'fetch', side_effect=[b'{"token":"mock"}', manifest, config]), self.assertRaises(ValueError):
            u.resolve_image('v2.9.6', 'amd64', 'a' * 40)

    def test_registry_digest_mismatch_rejected(self):
        manifest = json.dumps({'config': {'digest': 'sha256:' + '0' * 64}}).encode()
        with patch.object(u, 'fetch', side_effect=[b'{"token":"mock"}', manifest, b'{}']), self.assertRaises(ValueError):
            u.resolve_image('v2.9.6', 'amd64', 'a' * 40)

class RolloutTests(unittest.TestCase):
    def scenario(self, failure=None, no_op=False):
        import contextlib
        import tempfile
        from unittest.mock import mock_open
        state = workload()
        state['spec'].update(replicas=1, selector={'matchLabels': {'app': 'test'}})
        state['spec']['template']['spec']['containers'][0]['readinessProbe'] = {'httpGet': {'path': '/readyz', 'port': 8080}}
        old = copy.deepcopy(state['spec']['template'])
        image = u.IMAGE + '@sha256:' + 'c' * 64
        desired = u.replacement(state, image, 'd' * 64, 'sub2api')
        if no_op:
            state['spec']['template'] = copy.deepcopy(desired)
        mutations = []
        version = 'Sub2API 2.9.6 (commit: ' + 'a' * 40 + ', built: now)'
        rollouts = 0

        def fake_run(args, timeout=60):
            nonlocal rollouts
            if 'ctr' in args:
                if 'pull' in args:
                    if failure == 'pull':
                        raise RuntimeError('pull failed')
                    return ''
                return 'd' * 64 + '  /app/sub2api\n' + version
            if args[2:4] == ['get', 'nodes']:
                return json.dumps({'items': [{'status': {'nodeInfo': {'architecture': 'amd64'}}}]})
            if 'get' in args and 'deployment' in args:
                return json.dumps(state)
            if 'patch' in args:
                if '--dry-run=server' in args:
                    if failure == 'dry':
                        raise RuntimeError('admission rejected')
                    return ''
                ops = json.loads(Path(args[args.index('--patch-file') + 1]).read_text())
                self.assertEqual(ops[0]['value'], state['metadata']['resourceVersion'])
                self.assertEqual(ops[1]['value'], state['spec']['template'])
                state['spec']['template'] = copy.deepcopy(ops[2]['value'])
                state['metadata']['resourceVersion'] = str(int(state['metadata']['resourceVersion']) + 1)
                mutations.append(copy.deepcopy(ops))
                if failure == 'ambiguous' and len(mutations) == 1:
                    raise RuntimeError('HTTP response lost after API accepted patch')
                return ''
            if 'rollout' in args:
                rollouts += 1
                if failure == 'rollout' and rollouts == 1:
                    raise RuntimeError('not ready')
                if failure == 'concurrent' and rollouts == 1:
                    state['spec']['template']['metadata']['labels']['other'] = 'operator'
                return ''
            if 'get' in args and 'pods' in args:
                return json.dumps({'items': [{'metadata': {'name': 'pod'}, 'status': {
                    'containerStatuses': [{'name': 'sub2api', 'ready': True, 'imageID': image}],
                    'conditions': [{'type': 'Ready', 'status': 'True'}]}}]})
            if 'exec' in args:
                if '--version' in args:
                    return version if failure != 'version' else 'wrong version'
                return 'd' * 64 + '  /app/sub2api'
            raise AssertionError(args)

        with tempfile.TemporaryDirectory() as directory, contextlib.ExitStack() as stack:
            stack.enter_context(patch.object(u.sys, 'argv', ['upgrade', '--backup-root', directory, '--version', 'v2.9.6']))
            stack.enter_context(patch.object(u.os, 'geteuid', return_value=0))
            stack.enter_context(patch.object(u.fcntl, 'flock'))
            stack.enter_context(patch.object(u.Path, 'mkdir'))
            stack.enter_context(patch('builtins.open', mock_open()))
            stack.enter_context(patch.object(u, 'run', side_effect=fake_run))
            stack.enter_context(patch.object(u, 'resolve_release', return_value=('v2.9.6', 'a' * 40)))
            stack.enter_context(patch.object(u, 'resolve_image', return_value=image))
            if failure:
                with self.assertRaises((RuntimeError, ValueError)):
                    u.main()
            else:
                u.main()
        return old, state['spec']['template'], mutations

    def test_successful_rollout_preserves_template_configuration(self):
        old, current, mutations = self.scenario()
        self.assertEqual(len(mutations), 1)
        self.assertEqual(old['spec']['volumes'], current['spec']['volumes'])

    def test_noop_does_not_restart(self):
        _, _, mutations = self.scenario(no_op=True)
        self.assertEqual(mutations, [])

    def test_pull_and_admission_failure_do_not_mutate_workload(self):
        for failure in ['pull', 'dry']:
            old, current, mutations = self.scenario(failure)
            self.assertEqual(mutations, [])
            self.assertEqual(old, current)

    def test_failed_rollout_or_wrong_binary_restores_original_template(self):
        for failure in ['rollout', 'version', 'ambiguous']:
            old, current, mutations = self.scenario(failure)
            self.assertEqual(len(mutations), 2)
            self.assertEqual(old, current)

    def test_concurrent_change_is_not_overwritten_during_rollback(self):
        _, current, mutations = self.scenario('concurrent')
        self.assertEqual(len(mutations), 1)
        self.assertEqual(current['metadata']['labels']['other'], 'operator')


if __name__ == '__main__':
    unittest.main()
