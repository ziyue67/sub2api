#!/usr/bin/env python3
"""Upgrade an existing single-node k3s Deployment to a stable release."""
import argparse
import copy
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import urllib.parse
import urllib.request
import uuid

REPO = 'ranxi2001/sub2api'
IMAGE = 'ghcr.io/' + REPO
VERSION = re.compile(r'^v[0-9]+[.][0-9]+[.][0-9]+$')
SHA = re.compile(r'^[0-9a-f]{40}$')
DIGEST = re.compile(r'^sha256:[0-9a-f]{64}$')
CHECKSUM = re.compile(r'(?<![0-9a-f])([0-9a-f]{64})(?= +/app/sub2api)')


def run(args, timeout=60):
    p = subprocess.run(args, capture_output=True, text=True, timeout=timeout)
    if p.returncode:
        raise RuntimeError('Command failed: ' + ' '.join(args[:5]) + '; output withheld')
    return p.stdout


class SafeRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        if urllib.parse.urlparse(newurl).scheme != 'https':
            raise ValueError('Refusing non-HTTPS metadata redirect')
        redirected = super().redirect_request(request, fp, code, msg, headers, newurl)
        if redirected and urllib.parse.urlparse(newurl).netloc != urllib.parse.urlparse(request.full_url).netloc:
            redirected.remove_header('Authorization')
        return redirected


def fetch(url, headers=None):
    request = urllib.request.Request(url, headers={'User-Agent': 'sub2api-upgrade', **(headers or {})})
    with urllib.request.build_opener(SafeRedirect()).open(request, timeout=30) as response:
        raw = response.read(4 * 1024 * 1024 + 1)
    if len(raw) > 4 * 1024 * 1024:
        raise ValueError('Metadata exceeds size limit')
    return raw


def api(path):
    return json.loads(fetch('https://api.github.com/repos/' + REPO + '/' + path))


def resolve_release(version):
    if version != 'latest' and not VERSION.fullmatch(version):
        raise ValueError('Use latest or a stable tag such as v2.9.6')
    release = api('releases/latest' if version == 'latest' else 'releases/tags/' + version)
    tag = release['tag_name']
    if release['draft'] or release['prerelease'] or not VERSION.fullmatch(tag):
        raise ValueError('Only published stable releases are supported')
    if version != 'latest' and version != tag:
        raise ValueError('Release tag mismatch')
    obj = api('git/ref/tags/' + tag)['object']
    for _ in range(8):
        if obj['type'] == 'commit':
            break
        if obj['type'] != 'tag' or not SHA.fullmatch(obj['sha']):
            raise ValueError('Invalid tag object')
        obj = api('git/tags/' + obj['sha'])['object']
    if obj['type'] != 'commit' or not SHA.fullmatch(obj['sha']):
        raise ValueError('Cannot resolve release commit')
    return tag, obj['sha']


def resolve_image(tag, arch, commit):
    query = urllib.parse.urlencode({'service': 'ghcr.io', 'scope': 'repository:' + REPO + ':pull'})
    token = json.loads(fetch('https://ghcr.io/token?' + query))['token']
    headers = {'Authorization': 'Bearer ' + token, 'Accept': ', '.join([
        'application/vnd.oci.image.index.v1+json',
        'application/vnd.docker.distribution.manifest.list.v2+json',
        'application/vnd.oci.image.manifest.v1+json',
        'application/vnd.docker.distribution.manifest.v2+json'])}

    def get(kind, ref):
        raw = fetch('https://ghcr.io/v2/' + REPO + '/' + kind + '/' + ref, headers)
        digest = 'sha256:' + hashlib.sha256(raw).hexdigest()
        if ref.startswith('sha256:') and digest != ref:
            raise ValueError('OCI content digest mismatch')
        return json.loads(raw), digest

    manifest, digest = get('manifests', tag[1:])
    if 'manifests' in manifest:
        matches = [m for m in manifest['manifests'] if m.get('platform', {}).get('os') == 'linux'
                   and m.get('platform', {}).get('architecture') == arch]
        if len(matches) != 1 or not DIGEST.fullmatch(matches[0]['digest']):
            raise ValueError('Release must contain one matching Linux image')
        manifest, digest = get('manifests', matches[0]['digest'])
    ref = manifest['config']['digest']
    if not DIGEST.fullmatch(ref):
        raise ValueError('Invalid OCI config digest')
    config, _ = get('blobs', ref)
    labels = config.get('config', {}).get('Labels', {})
    if config.get('os') != 'linux' or config.get('architecture') != arch:
        raise ValueError('Image platform mismatch')
    if labels.get('org.opencontainers.image.revision') != commit or labels.get('org.opencontainers.image.version') != tag[1:]:
        raise ValueError('Image does not match release commit/version')
    return IMAGE + '@' + digest


def replacement(before, image, binary_sha, container_name):
    if not re.fullmatch(r'[0-9a-f]{64}', binary_sha):
        raise ValueError('Invalid binary checksum')
    after = copy.deepcopy(before['spec']['template'])
    matches = [c for c in after['spec']['containers'] if c['name'] == container_name]
    if len(matches) != 1:
        raise ValueError('Application container missing or ambiguous')
    old_image = matches[0]['image']
    matches[0]['image'] = image
    for c in after['spec'].get('initContainers', []):
        if c['image'] != old_image:
            if c['image'].startswith(IMAGE + '@') or c['image'].startswith(IMAGE + ':'):
                raise ValueError('Sub2API init image differs from the application')
            continue
        c['image'] = image
        count = 0
        for key in ('command', 'args'):
            if key not in c:
                continue
            updated = []
            for value in c[key]:
                value, n = CHECKSUM.subn(binary_sha, value)
                updated.append(value)
                count += n
            c[key] = updated
        if c['name'] == 'verify-runtime' and count != 1:
            raise ValueError('verify-runtime must contain one /app/sub2api checksum')
    return after


def guarded_patch(current, expected, desired):
    if current['spec']['template'] != expected:
        raise RuntimeError('Concurrent template change; refusing to overwrite it')
    return [
        {'op': 'test', 'path': '/metadata/resourceVersion', 'value': current['metadata']['resourceVersion']},
        {'op': 'test', 'path': '/spec/template', 'value': expected},
        {'op': 'replace', 'path': '/spec/template', 'value': desired},
    ]


def verify_version(output, tag, commit):
    pattern = r'Sub2API +' + re.escape(tag[1:]) + r' +[(]commit: *' + re.escape(commit) + r'[,)]'
    if not re.search(pattern, output):
        raise ValueError('Binary version/commit mismatch')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', default='latest')
    parser.add_argument('--namespace', default='tosky-canary')
    parser.add_argument('--deployment', default='tosky-nerd')
    parser.add_argument('--container', default='sub2api')
    parser.add_argument('--timeout', type=int, default=600)
    parser.add_argument('--backup-root', default='/var/backups/sub2api-k3s')
    parser.add_argument('--dry-run', action='store_true', help='Read/resolve only; no image pull or rollout')
    args = parser.parse_args()
    if not 30 <= args.timeout <= 3600:
        parser.error('--timeout must be 30..3600 seconds')
    if os.geteuid() != 0:
        parser.error('Run on the k3s host as root')
    os.umask(0o077)
    Path('/run/lock').mkdir(exist_ok=True)
    with open('/run/lock/sub2api-deploy.lock', 'a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        kubectl = ['k3s', 'kubectl', '-n', args.namespace]

        def read():
            return json.loads(run(kubectl + ['get', 'deployment', args.deployment, '-o', 'json']))

        before = read()
        if before['spec'].get('replicas', 1) != 1 or before['spec'].get('paused'):
            raise ValueError('Only active single-replica Deployments are supported')
        selector = before['spec']['selector']
        if selector.get('matchExpressions') or not selector.get('matchLabels'):
            raise ValueError('A matchLabels selector is required')
        app = next((c for c in before['spec']['template']['spec']['containers'] if c['name'] == args.container), None)
        if app is None or not app.get('readinessProbe'):
            raise ValueError('Application container must have an existing readiness probe')
        nodes = json.loads(run(['k3s', 'kubectl', 'get', 'nodes', '-o', 'json']))['items']
        if len(nodes) != 1:
            raise ValueError('This local pre-pull helper supports single-node k3s only')
        arch = nodes[0]['status']['nodeInfo']['architecture']
        if arch not in ('amd64', 'arm64'):
            raise ValueError('Unsupported architecture')
        tag, commit = resolve_release(args.version)
        image = resolve_image(tag, arch, commit)
        print(json.dumps({'tag': tag, 'commit': commit, 'image': image, 'namespace': args.namespace, 'deployment': args.deployment}), flush=True)
        if args.dry_run:
            return
        root = Path(args.backup_root)
        root.mkdir(parents=True, exist_ok=True, mode=0o700)
        backup = Path(tempfile.mkdtemp(prefix=tag + '-', dir=root))
        (backup / 'deployment-before.json').write_text(json.dumps(before))
        (backup / 'release.json').write_text(json.dumps({'tag': tag, 'commit': commit, 'image': image}))
        print('Backup: ' + str(backup), flush=True)
        run(['k3s', 'ctr', '-n', 'k8s.io', 'images', 'pull', '--platform', 'linux/' + arch, image], timeout=args.timeout)
        # No production volume or credentials; only inspect the packaged binary.
        output = run(['k3s', 'ctr', '-n', 'k8s.io', 'run', '--rm', image,
                      'sub2api-verify-' + uuid.uuid4().hex[:12], '/bin/sh', '-ec',
                      'sha256sum /app/sub2api; /app/sub2api --version'])
        verify_version(output, tag, commit)
        match = re.search(r'^([0-9a-f]{64}) +/app/sub2api *$', output, re.M)
        if not match:
            raise ValueError('Image binary checksum missing')
        binary_sha = match[1]
        desired = replacement(before, image, binary_sha, args.container)
        old_template = before['spec']['template']

        def patch(current, expected, target, dry=False):
            ops = guarded_patch(current, expected, target)
            path = backup / ('dry-run.json' if dry else 'patch.json')
            path.write_text(json.dumps(ops))
            return run(kubectl + ['patch', 'deployment', args.deployment, '--type=json', '--patch-file', str(path)]
                       + (['--dry-run=server'] if dry else []))

        def rollout():
            run(kubectl + ['rollout', 'status', 'deployment/' + args.deployment, '--timeout=' + str(args.timeout) + 's'], timeout=args.timeout + 15)

        def verify():
            rollout()
            if read()['spec']['template'] != desired:
                raise RuntimeError('Deployment changed during verification')
            labels = ','.join(k + '=' + v for k, v in selector['matchLabels'].items())
            pods = json.loads(run(kubectl + ['get', 'pods', '-l', labels, '-o', 'json']))['items']
            live = [p for p in pods if not p['metadata'].get('deletionTimestamp')]
            if len(live) != 1:
                raise ValueError('Expected one live Pod')
            pod = live[0]
            status = next(s for s in pod['status'].get('containerStatuses', []) if s['name'] == args.container)
            if not status['ready'] or status['imageID'].split('@')[-1] != image.split('@')[1]:
                raise ValueError('Pod readiness or imageID mismatch')
            exec_args = kubectl + ['exec', pod['metadata']['name'], '-c', args.container, '--']
            verify_version(run(exec_args + ['/app/sub2api', '--version']), tag, commit)
            if run(exec_args + ['sha256sum', '/app/sub2api']).split()[0] != binary_sha:
                raise ValueError('Running binary checksum mismatch')
            conditions = {c['type']: c['status'] for c in pod['status'].get('conditions', [])}
            if conditions.get('Ready') != 'True':
                raise ValueError('Pod is not Ready')
            (backup / 'verified.json').write_text(json.dumps({'pod': pod['metadata']['name'], 'imageID': status['imageID'], 'commit': commit, 'version': tag, 'binary_sha256': binary_sha}))

        mutation_attempted = False
        try:
            if desired != old_template:
                current = read()
                patch(current, old_template, desired, dry=True)
                mutation_attempted = True
                patch(current, old_template, desired)
            verify()
        except BaseException:
            if mutation_attempted:
                current = read()  # Resolve ambiguous HTTP timeout before rollback.
                if current['spec']['template'] == desired:
                    print('Verification failed; restoring saved template', file=sys.stderr, flush=True)
                    patch(current, desired, old_template)
                    rollout()
                    print('Previous deployment is available again', file=sys.stderr, flush=True)
                elif current['spec']['template'] != old_template:
                    print('Concurrent change: rollback refused. Backup: ' + str(backup), file=sys.stderr)
            raise
        print('Verified ' + tag + ' at ' + commit + '; backup=' + str(backup), flush=True)


if __name__ == '__main__':
    try:
        main()
    except Exception as exc:
        print('Upgrade failed: ' + type(exc).__name__ + ': ' + str(exc), file=sys.stderr)
        sys.exit(1)
