"""Bounded image reference validation for the project-file uploader."""
import base64
import hashlib
import ipaddress
import socket
import urllib.parse
import urllib.request

MAX_IMAGE_BYTES = 12 * 1024 * 1024

class ImageReferenceError(ValueError):
    pass

class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None

def _public(url):
    parsed = urllib.parse.urlparse(url)
    if parsed.scheme not in ('http', 'https') or not parsed.hostname:
        raise ImageReferenceError('image URL must use http or https')
    try:
        addresses = socket.getaddrinfo(parsed.hostname, parsed.port or 443)
    except OSError as exc:
        raise ImageReferenceError('image host cannot be resolved') from exc
    if any(not ipaddress.ip_address(addr[4][0].split('%', 1)[0]).is_global for addr in addresses):
        raise ImageReferenceError('image URL must resolve to a public address')

def resolve(reference, *, allow_local=False, timeout=15):
    if not isinstance(reference, str) or not reference.strip():
        raise ImageReferenceError('image reference is required')
    reference = reference.strip()
    if reference.startswith('data:'):
        header, sep, payload = reference.partition(',')
        if not sep or ';base64' not in header.lower():
            raise ImageReferenceError('image data must be base64 encoded')
        try:
            raw = base64.b64decode(payload, validate=True)
        except Exception as exc:
            raise ImageReferenceError('invalid image data') from exc
        mime = header[5:].split(';', 1)[0] or 'image/png'
    elif reference.startswith(('http://', 'https://')):
        _public(reference)
        try:
            with urllib.request.build_opener(_NoRedirect()).open(
                    urllib.request.Request(reference, headers={'Accept': 'image/*'}), timeout=timeout) as response:
                raw, mime = response.read(MAX_IMAGE_BYTES + 1), response.headers.get_content_type()
        except OSError as exc:
            raise ImageReferenceError('image download failed') from exc
    elif allow_local:
        from pathlib import Path
        path = Path(urllib.parse.unquote(reference.removeprefix('file://')))
        if path.suffix.lower() not in {'.png', '.jpg', '.jpeg', '.webp', '.gif'} or not path.is_file():
            raise ImageReferenceError('local image path is invalid')
        raw, mime = path.read_bytes(), 'image/' + path.suffix.lower().lstrip('.').replace('jpg', 'jpeg')
    else:
        raise ImageReferenceError('local image references are disabled')
    if not raw or len(raw) > MAX_IMAGE_BYTES:
        raise ImageReferenceError('image is empty or exceeds 12 MiB')
    return raw, mime or 'application/octet-stream'

def digest(raw):
    return hashlib.sha256(raw).hexdigest()
