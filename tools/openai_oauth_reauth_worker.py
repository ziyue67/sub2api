#!/usr/bin/env python3
"""Run queued sub2api OpenAI OAuth re-login jobs.

Email-link jobs use Turb's HTTP protocol flow. Password/TOTP jobs use a local
toSub2 checkout selected by TOSUB2_ROOT. The worker never starts a browser.
"""

from __future__ import annotations

import argparse
import base64
import html
import ipaddress
import json
import logging
import os
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from html.parser import HTMLParser
from pathlib import Path
from types import SimpleNamespace
from typing import Any, Callable
from urllib.error import HTTPError, URLError
from urllib.parse import urlparse
from urllib.request import HTTPRedirectHandler, Request, build_opener


LOGGER = logging.getLogger("openai_reauth_worker")
MAX_RESPONSE_BYTES = 1_048_576
CODE_RE = re.compile(r"\b(\d{6})\b")
URL_RE = re.compile(r"https?://[^\s]+", re.IGNORECASE)
SENSITIVE_PARAM_RE = re.compile(
    r"(?i)(code|state|key|token|secret|password)=([^&\s]+)"
)
SENSITIVE_JSON_RE = re.compile(
    r'''(?i)(["']?(?:access_token|refresh_token|id_token|token|code|state|key|secret|password)["']?\s*:\s*)["']?[^"'\s,}]+'''
)
EMAIL_RE = re.compile(r"(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b")
JWT_RE = re.compile(r"\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]*)?\b")
BEARER_RE = re.compile(r"(?i)\bBearer\s+\S+")
CONTEXT_CODE_RE = re.compile(
    r"(?i)(?:verification|verify|login|temporary|验证码|登录代码|認証|code)"
    r"[^0-9]{0,80}(\d{6})"
)


class WorkerError(RuntimeError):
    pass


class ProtocolHTTPError(WorkerError):
    def __init__(self, stage: str, status_code: int, url: str):
        self.status_code = status_code
        self.endpoint = _safe_openai_endpoint(url)
        super().__init__(f"{stage} returned HTTP {status_code} at {self.endpoint}")


class _NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


NO_REDIRECT_OPENER = build_opener(_NoRedirect)


def _safe_openai_endpoint(url: str) -> str:
    try:
        parsed = urlparse(str(url or ""))
    except ValueError:
        return "[redacted endpoint]"
    if (
        parsed.scheme != "https"
        or (parsed.hostname or "").lower() not in {"auth.openai.com", "sentinel.openai.com", "chatgpt.com"}
        or parsed.port not in (None, 443)
        or parsed.username
        or parsed.password
    ):
        return "[redacted endpoint]"
    return f"{parsed.hostname.lower()}{(parsed.path or '/')[:160]}"


def _checked_bootstrap_authorize(
    session: Any,
    bootstrap: Callable[..., Any],
    state: str,
    *,
    auth_url: str,
) -> None:
    original_get = session.get

    def checked_get(url: str, *args: Any, **kwargs: Any) -> Any:
        response = original_get(url, *args, **kwargs)
        status_code = int(getattr(response, "status_code", 0) or 0)
        if status_code >= 400:
            raise ProtocolHTTPError(
                "OpenAI OAuth bootstrap",
                status_code,
                str(getattr(response, "url", "") or url),
            )
        return response

    session.get = checked_get
    try:
        bootstrap(session, state, auth_url=auth_url)
    finally:
        session.get = original_get


class _PasswordlessIntentParser(HTMLParser):
    def __init__(self) -> None:
        super().__init__()
        self.found = False

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        values = {key.lower(): value or "" for key, value in attrs}
        if tag in {"button", "input"} and values.get("name") == "intent" and values.get("value") == "passwordless_login_send_otp":
            self.found = True


def _has_passwordless_intent(html_body: str) -> bool:
    parser = _PasswordlessIntentParser()
    parser.feed(html_body or "")
    return parser.found


def _start_passwordless_login(session: Any, continue_url: str) -> str:
    parsed = urlparse(continue_url)
    if (
        parsed.scheme != "https"
        or parsed.hostname != "auth.openai.com"
        or parsed.port not in (None, 443)
        or parsed.username
        or parsed.password
    ):
        raise WorkerError("OpenAI passwordless login URL is invalid")
    response = session.get(
        continue_url,
        headers=session.get_auth_navigate_headers(referer="https://auth.openai.com/log-in"),
        allow_redirects=True,
    )
    if response.status_code >= 400:
        raise WorkerError(f"OpenAI password page returned HTTP {response.status_code}")
    page_url = str(getattr(response, "url", "") or continue_url)
    page = urlparse(page_url)
    if page.scheme != "https" or page.hostname != "auth.openai.com" or page.port not in (None, 443):
        raise WorkerError("OpenAI password page redirected to an invalid URL")
    if not _has_passwordless_intent(response.text or ""):
        raise WorkerError("OpenAI passwordless login intent was not found")

    headers = session.get_auth_headers(referer=page_url)
    headers["accept"] = "application/json"
    headers["content-type"] = "application/json"
    result = session.post(
        "https://auth.openai.com/api/accounts/passwordless/send-otp",
        headers=headers,
        allow_redirects=False,
    )
    if result.status_code >= 400:
        raise WorkerError(f"OpenAI passwordless OTP request returned HTTP {result.status_code}")
    try:
        payload = result.json()
    except ValueError:
        raise WorkerError("OpenAI passwordless OTP response was not JSON") from None
    page_type = str((payload.get("page") or {}).get("type") or "") if isinstance(payload, dict) else ""
    if page_type not in {"email_otp_verification", "email_otp_verification_registration"}:
        safe_page_type = re.sub(r"[^A-Za-z0-9_-]", "", page_type)[:80] or "unknown"
        raise WorkerError(f"OpenAI passwordless OTP returned an unexpected page ({safe_page_type})")
    LOGGER.info("passwordless OTP requested status=%s page=%s", result.status_code, page_type)
    return "/email-verification"


def _positive_float(raw: str | None, default: float) -> float:
    try:
        value = float(str(raw or "").strip())
    except ValueError:
        return default
    return value if value > 0 else default


def _trusted_otp_hosts() -> set[str]:
    return {
        host.strip().lower().rstrip(".")
        for host in os.getenv("OPENAI_REAUTH_TRUSTED_OTP_HOSTS", "").split(",")
        if host.strip()
    }


def validate_otp_url(url: str) -> None:
    parsed = urlparse(url)
    host = (parsed.hostname or "").lower().rstrip(".")
    if parsed.scheme not in {"http", "https"} or not host or parsed.username or parsed.password or parsed.fragment:
        raise WorkerError("OTP mailbox URL is invalid")
    loopback_name = host == "localhost"
    if parsed.scheme == "http" and not loopback_name:
        try:
            loopback_name = ipaddress.ip_address(host).is_loopback
        except ValueError:
            loopback_name = False
    if parsed.scheme == "http" and not loopback_name:
        raise WorkerError("OTP mailbox URL must use HTTPS")
    try:
        addresses = {item[4][0] for item in socket.getaddrinfo(host, parsed.port or (443 if parsed.scheme == "https" else 80))}
    except OSError:
        raise WorkerError("OTP mailbox hostname could not be resolved") from None
    trusted_private_host = host in _trusted_otp_hosts()
    for address in addresses:
        ip = ipaddress.ip_address(address)
        if loopback_name and ip.is_loopback:
            continue
        if not ip.is_global and not trusted_private_host:
            raise WorkerError("OTP mailbox hostname resolves to a non-public address")


@dataclass(frozen=True)
class WorkerConfig:
    base_url: str
    worker_token: str
    worker_id: str
    protocol_root: Path
    tosub2_root: Path | None = None
    poll_seconds: float = 5.0
    request_timeout: float = 30.0

    @classmethod
    def from_env(cls) -> "WorkerConfig":
        base_url = os.getenv("SUB2API_BASE_URL", "http://127.0.0.1:8080").strip().rstrip("/")
        parsed = urlparse(base_url)
        host = (parsed.hostname or "").lower()
        if parsed.scheme not in {"http", "https"} or not host or parsed.username or parsed.password or parsed.query or parsed.fragment:
            raise WorkerError("SUB2API_BASE_URL must be an absolute HTTP(S) URL")
        if parsed.scheme == "http" and host not in {"localhost", "127.0.0.1", "::1"}:
            raise WorkerError("SUB2API_BASE_URL must use HTTPS unless it targets localhost")

        worker_token = os.getenv("OPENAI_REAUTH_WORKER_TOKEN", "").strip()
        if len(worker_token) < 32:
            raise WorkerError("OPENAI_REAUTH_WORKER_TOKEN must contain at least 32 characters")

        worker_id = os.getenv("OPENAI_REAUTH_WORKER_ID", "").strip()
        if not worker_id:
            worker_id = f"{socket.gethostname()}-{os.getpid()}"
        if len(worker_id) > 128:
            raise WorkerError("OPENAI_REAUTH_WORKER_ID must be at most 128 characters")

        root_raw = (os.getenv("CODEX_PROTOCOL_ROOT", "").strip() or os.getenv("TURB_ROOT", "").strip())
        protocol_root = Path(root_raw).expanduser().resolve() if root_raw else Path()
        required = (
            protocol_root / "core" / "codex_oauth.py",
            protocol_root / "core" / "session.py",
            protocol_root / "sentinel" / "sentinel-runner.js",
            protocol_root / "sentinel" / "sdk.js",
        )
        if not root_raw or not all(path.is_file() for path in required):
            raise WorkerError(
                "CODEX_PROTOCOL_ROOT/TURB_ROOT must point to a Turb project with the pure protocol and Sentinel assets"
            )

        tosub2_raw = os.getenv("TOSUB2_ROOT", "").strip()
        tosub2_root = Path(tosub2_raw).expanduser().resolve() if tosub2_raw else None
        if tosub2_root is not None:
            required_tosub2 = (
                tosub2_root / "src" / "protocol-login.mjs",
                tosub2_root / "src" / "tls-transport.mjs",
            )
            if not all(path.is_file() for path in required_tosub2):
                raise WorkerError("TOSUB2_ROOT must point to a toSub2 checkout containing src/protocol-login.mjs")

        return cls(
            base_url=base_url,
            worker_token=worker_token,
            worker_id=worker_id,
            protocol_root=protocol_root,
            tosub2_root=tosub2_root,
            poll_seconds=_positive_float(os.getenv("OPENAI_REAUTH_POLL_SECONDS"), 5.0),
            request_timeout=_positive_float(os.getenv("OPENAI_REAUTH_REQUEST_TIMEOUT"), 30.0),
        )


class WorkerAPI:
    def __init__(self, config: WorkerConfig):
        self.config = config

    def _post(self, path: str, payload: dict[str, Any]) -> Any:
        request = Request(
            self.config.base_url + path,
            data=json.dumps(payload).encode("utf-8"),
            method="POST",
            headers={
                "Content-Type": "application/json",
                "Accept": "application/json",
                "X-OpenAI-Reauth-Worker-Token": self.config.worker_token,
            },
        )
        try:
            with NO_REDIRECT_OPENER.open(request, timeout=self.config.request_timeout) as response:
                body = response.read(MAX_RESPONSE_BYTES + 1)
        except HTTPError as exc:
            message = ""
            try:
                parsed = json.loads(exc.read(MAX_RESPONSE_BYTES).decode("utf-8", errors="replace"))
                message = str(parsed.get("message") or parsed.get("reason") or "")
            except (ValueError, AttributeError):
                pass
            raise WorkerError(f"sub2api HTTP {exc.code}: {message or 'request failed'}") from None
        except (URLError, TimeoutError, OSError) as exc:
            raise WorkerError(f"sub2api request failed: {type(exc).__name__}") from None
        if len(body) > MAX_RESPONSE_BYTES:
            raise WorkerError("sub2api response is too large")
        try:
            envelope = json.loads(body.decode("utf-8"))
        except (UnicodeDecodeError, ValueError):
            raise WorkerError("sub2api returned invalid JSON") from None
        if not isinstance(envelope, dict) or envelope.get("code") != 0:
            raise WorkerError(str(envelope.get("message") or "sub2api request failed"))
        return envelope.get("data")

    def claim(self) -> dict[str, Any] | None:
        data = self._post(
            "/api/v1/internal/openai-reauth/claim",
            {"worker_id": self.config.worker_id},
        )
        return data if isinstance(data, dict) else None

    def progress(self, task_id: int, stage: str) -> None:
        self._post(
            f"/api/v1/internal/openai-reauth/{task_id}/progress",
            {"worker_id": self.config.worker_id, "stage": stage},
        )

    def callback(self, task_id: int, callback_url: str) -> dict[str, Any] | None:
        data = self._post(
            f"/api/v1/internal/openai-reauth/{task_id}/callback",
            {"worker_id": self.config.worker_id, "callback_url": callback_url},
        )
        return data if isinstance(data, dict) else None

    def credentials(
        self,
        task_id: int,
        credentials: dict[str, Any],
        extra: dict[str, Any] | None = None,
    ) -> dict[str, Any] | None:
        data = self._post(
            f"/api/v1/internal/openai-reauth/{task_id}/credentials",
            {
                "worker_id": self.config.worker_id,
                "credentials": credentials,
                "extra": extra or {},
            },
        )
        return data if isinstance(data, dict) else None

    def fail(self, task_id: int, reason: str) -> None:
        self._post(
            f"/api/v1/internal/openai-reauth/{task_id}/fail",
            {"worker_id": self.config.worker_id, "reason": sanitize_error(reason)},
        )


def _parse_timestamp(value: Any) -> float | None:
    if isinstance(value, (int, float)):
        timestamp = float(value)
        return timestamp / 1000 if timestamp > 10_000_000_000 else timestamp
    raw = str(value or "").strip()
    if not raw:
        return None
    try:
        timestamp = float(raw)
        return timestamp / 1000 if timestamp > 10_000_000_000 else timestamp
    except ValueError:
        pass
    try:
        return datetime.fromisoformat(raw.replace("Z", "+00:00")).astimezone(timezone.utc).timestamp()
    except ValueError:
        return None


def _structured_otp(value: Any, after_ts: float | None) -> str:
    if isinstance(value, list):
        for item in reversed(value):
            code = _structured_otp(item, after_ts)
            if code:
                return code
        return ""
    if not isinstance(value, dict):
        return ""

    timestamp = next(
        (
            _parse_timestamp(value.get(key))
            for key in (
                "time",
                "date",
                "received_at",
                "receivedAt",
                "created_at",
                "createdAt",
                "timestamp",
            )
            if value.get(key) is not None
        ),
        None,
    )
    if after_ts and timestamp and timestamp + 2 < after_ts:
        return ""

    for key in ("code", "otp", "verification_code", "verificationCode", "email_code", "emailCode"):
        match = CODE_RE.search(str(value.get(key) or ""))
        if match:
            return match.group(1)

    for nested in value.values():
        code = _structured_otp(nested, after_ts)
        if code:
            return code
    return ""


def extract_otp(body: str, after_ts: float | None = None) -> str:
    try:
        parsed = json.loads(body)
        code = _structured_otp(parsed, after_ts)
        if code:
            return code
        if after_ts and isinstance(parsed, dict):
            has_timestamp = any(
                parsed.get(key) is not None
                for key in ("time", "date", "received_at", "receivedAt", "created_at", "createdAt", "timestamp")
            )
            has_code = any(parsed.get(key) is not None for key in ("code", "otp", "verification_code", "verificationCode", "email_code", "emailCode"))
            if has_timestamp and has_code:
                return ""
    except ValueError:
        pass

    text = re.sub(r"(?is)<(?:style|script)[^>]*>.*?</(?:style|script)>", " ", body or "")
    text = html.unescape(re.sub(r"(?s)<[^>]+>", " ", text))
    contextual = CONTEXT_CODE_RE.findall(text)
    if contextual:
        return contextual[-1]
    candidates = [code for code in CODE_RE.findall(text) if code not in {"000000", "202123", "353740"}]
    return candidates[-1] if candidates else ""


class OTPURLProvider:
    def __init__(
        self,
        url: str,
        timeout: float = 20.0,
        on_wait: Callable[[], None] | None = None,
    ):
        validate_otp_url(url)
        self.url = url
        self.timeout = timeout
        self.on_wait = on_wait
        self._announced = False

    def __call__(self, _email: str, after_ts: float | None = None) -> str:
        if self.on_wait and not self._announced:
            self._announced = True
            self.on_wait()
        validate_otp_url(self.url)
        request = Request(
            self.url,
            headers={
                "Accept": "application/json,text/html,text/plain,*/*",
                "User-Agent": "sub2api-openai-reauth-worker/1.0",
            },
        )
        try:
            with NO_REDIRECT_OPENER.open(request, timeout=self.timeout) as response:
                body = response.read(MAX_RESPONSE_BYTES + 1)
        except HTTPError as exc:
            raise WorkerError(f"OTP mailbox returned HTTP {exc.code}") from None
        except (URLError, TimeoutError, OSError) as exc:
            raise WorkerError(f"OTP mailbox request failed: {type(exc).__name__}") from None
        if len(body) > MAX_RESPONSE_BYTES:
            raise WorkerError("OTP mailbox response is too large")
        return extract_otp(body.decode("utf-8", errors="replace"), after_ts)


def load_protocol(root: Path) -> SimpleNamespace:
    node_executable = os.getenv("NODE_EXECUTABLE", "node").strip()
    if not node_executable or shutil.which(node_executable) is None:
        raise WorkerError("Node.js is required for the protocol Sentinel runner")

    root_text = str(root)
    if root_text not in sys.path:
        sys.path.insert(0, root_text)

    # Turb error logs can include provider response bodies. Suppress every
    # Turb log level before importing or executing its protocol modules.
    quiet_level = logging.CRITICAL + 1
    logging.getLogger("core").setLevel(quiet_level)
    logging.getLogger("config").setLevel(quiet_level)

    from core.codex_oauth import (
        _bootstrap_authorize,
        _extract_code,
        _follow_until_callback,
        _is_redirect_uri,
        _post_json,
        _resp_json,
        _select_workspace_and_get_callback,
        build_sentinel_header,
        network_preflight,
        request_sentinel_token,
    )
    from core.openai_auth import (
        AccountUnusableError,
        _extract_error_code,
        detect_account_unusable_response_body,
        send_email_otp,
    )
    # Turb keeps this class name for historical reasons; it is a curl_cffi
    # HTTP session and never launches a browser.
    from core.session import BrowserSession as ProtocolSession

    def submit_email(session: Any, email: str) -> dict[str, Any]:
        sentinel = request_sentinel_token(session, "authorize_continue")
        sentinel_header, so_header = build_sentinel_header(session, sentinel, "authorize_continue")
        response = _post_json(
            session,
            "https://auth.openai.com/api/accounts/authorize/continue",
            {"username": {"kind": "email", "value": email}},
            referer="https://auth.openai.com/log-in",
            sentinel_header=sentinel_header,
            so_header=so_header,
        )
        if response.status_code not in (200, 204):
            raise WorkerError(f"OpenAI email submission returned HTTP {response.status_code}")
        try:
            payload = response.json()
        except ValueError:
            return {}
        return payload if isinstance(payload, dict) else {}

    def submit_email_otp(session: Any, code: str) -> None:
        sentinel = request_sentinel_token(session, "authorize_continue")
        sentinel_header, so_header = build_sentinel_header(session, sentinel, "authorize_continue")
        response = _post_json(
            session,
            "https://auth.openai.com/api/accounts/email-otp/validate",
            {"code": code},
            referer="https://auth.openai.com/email-verification",
            sentinel_header=sentinel_header,
            so_header=so_header,
        )
        if response.status_code != 200:
            error_code = _extract_error_code(response)
            if error_code in ("account_deactivated", "account_deleted", "account_banned"):
                raise AccountUnusableError(
                    f"[Codex] 账号已废（{error_code}）status={response.status_code}: {(response.text or '')[:200]}",
                    error_code=error_code,
                )
            body_error_code = detect_account_unusable_response_body(response.text or "")
            if body_error_code:
                raise AccountUnusableError(
                    f"[Codex] 账号已废（{body_error_code}）status={response.status_code}: {(response.text or '')[:200]}",
                    error_code=body_error_code,
                )
            raise RuntimeError(
                f"[Codex] 邮箱 OTP 验证失败 status={response.status_code}: {(response.text or '')[:300]}"
            )
        # OpenAI can return workspaces in the OTP JSON without setting the
        # oai-client-auth-session cookie expected by Turb's next step.
        workspace_id = _workspace_id_from_auth_payload(_resp_json(response))
        if workspace_id:
            session._sub2api_workspace_id = workspace_id

    def select_workspace_and_get_callback(session: Any, state: str) -> str:
        workspace_id = str(getattr(session, "_sub2api_workspace_id", "") or "").strip()
        if not workspace_id:
            return _select_workspace_and_get_callback(session, state)
        response = _post_json(
            session,
            "https://auth.openai.com/api/accounts/workspace/select",
            {"workspace_id": workspace_id},
            referer="https://auth.openai.com/sign-in-with-chatgpt/codex/consent",
        )
        location = response.headers.get("location") or response.headers.get("Location")
        if location and _is_redirect_uri(location):
            return location
        data = _resp_json(response)
        next_url = ""
        for key in ("redirect_url", "continue_url", "url", "next", "location"):
            value = data.get(key)
            if isinstance(value, str) and value:
                next_url = value
                break
        if not next_url and location:
            next_url = location
        if not next_url:
            raise RuntimeError(
                f"[Codex] workspace/select 后找不到下一跳 URL: status={response.status_code}, "
                f"body={(response.text or '')[:300]}"
            )
        return _follow_until_callback(session, next_url, state)

    return SimpleNamespace(
        ProtocolSession=ProtocolSession,
        AccountUnusableError=AccountUnusableError,
        network_preflight=network_preflight,
        bootstrap_authorize=lambda session, state, auth_url=None: _checked_bootstrap_authorize(
            session,
            _bootstrap_authorize,
            state,
            auth_url=str(auth_url or ""),
        ),
        submit_email=submit_email,
        start_passwordless_login=_start_passwordless_login,
        resend_email_otp=send_email_otp,
        submit_email_otp=submit_email_otp,
        select_workspace_and_get_callback=select_workspace_and_get_callback,
        extract_code=_extract_code,
    )


def _best_effort_progress(api: WorkerAPI, task_id: int, stage: str) -> None:
    try:
        api.progress(task_id, stage)
    except WorkerError as exc:
        LOGGER.warning("task=%s progress=%s failed: %s", task_id, stage, sanitize_error(str(exc)))


def sanitize_error(value: str) -> str:
    text = URL_RE.sub("[redacted URL]", str(value or ""))
    text = SENSITIVE_PARAM_RE.sub(lambda match: f"{match.group(1)}=[redacted]", text)
    text = SENSITIVE_JSON_RE.sub(lambda match: f"{match.group(1)}[redacted]", text)
    text = EMAIL_RE.sub("[redacted email]", text)
    text = JWT_RE.sub("[redacted token]", text)
    text = BEARER_RE.sub("Bearer [redacted]", text)
    text = CODE_RE.sub("[redacted OTP]", text)
    text = re.sub(r"(?is)<(?:style|script)[^>]*>.*?</(?:style|script)>", " ", text)
    text = html.unescape(re.sub(r"(?s)<[^>]+>", " ", text))
    text = re.sub(r"\s+", " ", text).strip()
    return (text or "automatic re-login failed")[:500]


def _close_protocol_session(session: Any) -> None:
    raw_session = getattr(session, "session", None)
    close = getattr(raw_session, "close", None)
    if callable(close):
        close()


def _auth_session_flow(session: Any) -> dict[str, Any]:
    raw = ""
    try:
        for cookie in session.session.cookies.jar:
            if cookie.name == "oai-client-auth-session":
                raw = str(cookie.value or "")
                break
    except (AttributeError, TypeError):
        return {}
    try:
        encoded = raw.split(".", 1)[0]
        payload = json.loads(base64.urlsafe_b64decode(encoded + "=" * (-len(encoded) % 4)))
    except (ValueError, TypeError):
        return {}
    if not isinstance(payload, dict):
        return {}
    return {
        key: payload[key]
        for key in ("email_verification_mode", "passwordless_disabled", "signup_mode")
        if key in payload
    }


def _workspace_id_from_auth_payload(payload: Any) -> str:
    if not isinstance(payload, dict):
        return ""
    auth_session = payload.get("oai-client-auth-session")
    if not isinstance(auth_session, dict):
        return ""
    workspaces = auth_session.get("workspaces")
    if not isinstance(workspaces, list):
        return ""
    for workspace in workspaces:
        if isinstance(workspace, dict):
            workspace_id = str(workspace.get("id") or "").strip()
            if workspace_id:
                return workspace_id
    return ""


def _email_submission_state(payload: Any) -> tuple[str, str]:
    if not isinstance(payload, dict):
        return "unknown", ""
    page = payload.get("page")
    page = page if isinstance(page, dict) else {}
    page_type = str(page.get("type") or payload.get("page_type") or "unknown")[:80]
    continue_url = str(
        payload.get("continue_url")
        or payload.get("external_url")
        or payload.get("url")
        or page.get("continue_url")
        or page.get("external_url")
        or page.get("url")
        or ""
    )
    try:
        path = urlparse(continue_url).path[:160]
    except ValueError:
        path = ""
    return page_type, path


def _email_submission_url(payload: Any) -> str:
    if not isinstance(payload, dict):
        return ""
    page = payload.get("page")
    page = page if isinstance(page, dict) else {}
    return str(
        payload.get("continue_url")
        or payload.get("external_url")
        or payload.get("url")
        or page.get("continue_url")
        or page.get("external_url")
        or page.get("url")
        or ""
    )


def _extract_state(auth_url: str) -> str:
    parsed = urlparse(auth_url)
    query = parsed.query
    for item in query.split("&"):
        key, separator, value = item.partition("=")
        if separator and key == "state":
            from urllib.parse import unquote_plus

            return unquote_plus(value)
    return ""


def _is_local_codex_callback(url: str) -> bool:
    parsed = urlparse(url)
    return (
        parsed.scheme == "http"
        and parsed.netloc.lower() == "localhost:1455"
        and parsed.path == "/auth/callback"
        and not parsed.params
        and not parsed.fragment
    )


def _wait_for_otp(
    provider: OTPURLProvider,
    email: str,
    after_ts: float,
    *,
    timeout: float,
    interval: float,
    excluded_codes: set[str] | None = None,
) -> str:
    excluded_codes = excluded_codes or set()
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        code = provider(email, after_ts=after_ts)
        if code and code not in excluded_codes:
            return code
        remaining = deadline - time.monotonic()
        if remaining > 0:
            time.sleep(min(interval, remaining))
    raise WorkerError("timed out waiting for a new mailbox verification code")


def process_claim(api: WorkerAPI, protocol: SimpleNamespace, claim: dict[str, Any]) -> None:
    mode = str(claim.get("credential_mode") or "email_otp_url").strip()
    if mode == "password_totp":
        process_password_claim(api, claim)
        return
    if mode != "email_otp_url":
        raise WorkerError("claimed task has an unsupported credential mode")

    task_id = int(claim.get("task_id") or 0)
    account_id = int(claim.get("account_id") or 0)
    email = str(claim.get("login_email") or "").strip()
    otp_url = str(claim.get("otp_url") or "").strip()
    auth_url = str(claim.get("auth_url") or "").strip()
    proxy_url = str(claim.get("proxy_url") or "").strip()
    if task_id <= 0 or account_id <= 0 or not email or not otp_url or not auth_url:
        raise WorkerError("claimed task is missing required fields")

    state = _extract_state(auth_url)
    if not state:
        raise WorkerError("authorization URL is missing OAuth state")

    LOGGER.info("task=%s account=%s started (protocol)", task_id, account_id)
    _best_effort_progress(api, task_id, "starting")
    session = None
    try:
        _best_effort_progress(api, task_id, "protocol_connecting")
        # An empty proxy explicitly means direct connection.  Do not let the
        # Turb project silently choose a local proxy pool for this worker.
        for bootstrap_attempt in (1, 2):
            session = protocol.ProtocolSession(
                proxy=proxy_url or "",
                detect_exit_geo=bool(proxy_url),
                use_exit_geo_cache=False,
            )
            try:
                protocol.network_preflight(session)
                protocol.bootstrap_authorize(session, state, auth_url=auth_url)
                break
            except ProtocolHTTPError as exc:
                _close_protocol_session(session)
                session = None
                if exc.status_code != 403 or bootstrap_attempt == 2:
                    raise
                LOGGER.warning(
                    "task=%s OAuth bootstrap received HTTP 403 at %s; retrying once with a fresh protocol session",
                    task_id,
                    exc.endpoint,
                )
                time.sleep(1.0)
        otp_after_ts = time.time()
        submission = protocol.submit_email(session, email)
        page_type, continue_path = _email_submission_state(submission)
        flow = _auth_session_flow(session)
        LOGGER.info(
            "task=%s auth page=%s path=%s mode=%s passwordless_disabled=%s signup_mode=%s",
            task_id,
            page_type,
            continue_path or "unknown",
            flow.get("email_verification_mode", "unknown"),
            flow.get("passwordless_disabled", "unknown"),
            flow.get("signup_mode", "unknown"),
        )
        if page_type in {"login_password", "login-password", "password_login"} or continue_path == "/log-in/password":
            passwordless_url = _email_submission_url(submission)
            if not passwordless_url:
                raise WorkerError("OpenAI password page response did not include a continue URL")
            otp_after_ts = time.time()
            protocol.start_passwordless_login(session, passwordless_url)
            LOGGER.info("task=%s switched from password login to email OTP", task_id)
        _best_effort_progress(api, task_id, "email_submitted")
        otp_provider = OTPURLProvider(otp_url)
        max_otp_attempts = 3
        attempted_codes: set[str] = set()
        for attempt in range(1, max_otp_attempts + 1):
            try:
                _best_effort_progress(api, task_id, "waiting_otp")
                code = _wait_for_otp(
                    otp_provider,
                    email,
                    otp_after_ts,
                    timeout=180.0,
                    interval=2.0,
                    excluded_codes=attempted_codes,
                )
                attempted_codes.add(code)
                protocol.submit_email_otp(session, code)
                break
            except protocol.AccountUnusableError:
                raise
            except Exception as exc:
                if attempt >= max_otp_attempts:
                    raise
                LOGGER.warning("task=%s OTP attempt %s/%s failed: %s", task_id, attempt, max_otp_attempts, sanitize_error(str(exc)))
                protocol.resend_email_otp(session)
                otp_after_ts = time.time()
                _best_effort_progress(api, task_id, "email_submitted")
        _best_effort_progress(api, task_id, "otp_submitted")
        _best_effort_progress(api, task_id, "waiting_callback")
        callback_url = protocol.select_workspace_and_get_callback(session, state)
        if not _is_local_codex_callback(callback_url):
            raise WorkerError("Codex protocol returned an unexpected callback URL")
        protocol.extract_code(callback_url, state)
        result = api.callback(task_id, callback_url)
        if not result or result.get("status") != "succeeded":
            raise WorkerError(str((result or {}).get("error") or "callback was not accepted"))
        LOGGER.info("task=%s account=%s succeeded", task_id, account_id)
    finally:
        if session is not None:
            try:
                _close_protocol_session(session)
            except Exception as exc:
                LOGGER.warning("task=%s protocol session cleanup failed: %s", task_id, sanitize_error(str(exc)))


def process_password_claim(api: WorkerAPI, claim: dict[str, Any]) -> None:
    task_id = int(claim.get("task_id") or 0)
    account_id = int(claim.get("account_id") or 0)
    email = str(claim.get("login_email") or "").strip()
    password = str(claim.get("password") or "")
    totp_secret = str(claim.get("totp_secret") or "").strip()
    proxy_url = str(claim.get("proxy_url") or "").strip()
    root = getattr(getattr(api, "config", None), "tosub2_root", None)
    if task_id <= 0 or account_id <= 0 or not email or not password:
        raise WorkerError("claimed password task is missing required fields")
    if root is None:
        raise WorkerError("TOSUB2_ROOT is required for password/TOTP re-login")

    node_executable = os.getenv("NODE_EXECUTABLE", "node").strip()
    if not node_executable or shutil.which(node_executable) is None:
        raise WorkerError("Node.js is required for password/TOTP re-login")
    script = root / "src" / "protocol-login.mjs"
    if not script.is_file():
        raise WorkerError("toSub2 protocol-login.mjs is unavailable")

    LOGGER.info("task=%s account=%s started (password protocol)", task_id, account_id)
    _best_effort_progress(api, task_id, "starting")
    _best_effort_progress(api, task_id, "protocol_connecting")
    _best_effort_progress(api, task_id, "password_submitted")
    with tempfile.TemporaryDirectory(prefix="sub2api-reauth-") as temp_dir:
        output_path = Path(temp_dir) / "oauth.json"
        command = [
            node_executable,
            str(script),
            "--email",
            email,
            "--output-mode",
            "sub2api",
            "--sub2api-out",
            str(output_path),
            "--sub2api-name",
            f"credential-guard-{account_id}",
        ]
        if proxy_url:
            command.extend(("--proxy", proxy_url))
        child_env = os.environ.copy()
        child_env.pop("OPENAI_REAUTH_WORKER_TOKEN", None)
        child_env["CHATGPT_LOGIN_PASSWORD"] = password
        if totp_secret:
            child_env["CHATGPT_TOTP_SECRET"] = totp_secret
        else:
            child_env.pop("CHATGPT_TOTP_SECRET", None)
        try:
            result = subprocess.run(
                command,
                cwd=root,
                env=child_env,
                stdin=subprocess.DEVNULL,
                capture_output=True,
                text=True,
                timeout=25 * 60,
                check=False,
            )
        except subprocess.TimeoutExpired:
            raise WorkerError("password/TOTP protocol timed out") from None
        if result.returncode != 0:
            # The external runner may echo unlabeled secrets that regexes cannot redact.
            raise WorkerError(f"password/TOTP protocol failed (exit {result.returncode})")
        if totp_secret:
            _best_effort_progress(api, task_id, "mfa_submitted")
        try:
            payload = json.loads(output_path.read_text(encoding="utf-8"))
        except (OSError, UnicodeDecodeError, ValueError):
            raise WorkerError("password/TOTP protocol did not produce valid OAuth credentials") from None

    accounts = payload.get("accounts") if isinstance(payload, dict) else None
    account = accounts[0] if isinstance(accounts, list) and len(accounts) == 1 else None
    credentials = account.get("credentials") if isinstance(account, dict) else None
    extra = account.get("extra") if isinstance(account, dict) else None
    if not isinstance(credentials, dict) or not all(
        str(credentials.get(key) or "").strip()
        for key in ("access_token", "refresh_token", "id_token")
    ):
        raise WorkerError("password/TOTP protocol returned incomplete OAuth credentials")
    result = api.credentials(task_id, credentials, extra if isinstance(extra, dict) else {})
    if not result or result.get("status") != "succeeded":
        raise WorkerError(str((result or {}).get("error") or "credentials were not accepted"))
    LOGGER.info("task=%s account=%s succeeded", task_id, account_id)


def run_once(api: WorkerAPI, protocol: SimpleNamespace) -> bool:
    try:
        claim = api.claim()
    except WorkerError as exc:
        LOGGER.warning("claim failed: %s", sanitize_error(str(exc)))
        return False
    if not claim:
        return False
    task_id = int(claim.get("task_id") or 0)
    try:
        process_claim(api, protocol, claim)
    except Exception as exc:
        reason = sanitize_error(f"{type(exc).__name__}: {exc}")
        LOGGER.error("task=%s failed: %s", task_id or "?", reason)
        if task_id > 0:
            try:
                api.fail(task_id, reason)
            except WorkerError as fail_exc:
                LOGGER.error("task=%s failure report rejected: %s", task_id, sanitize_error(str(fail_exc)))
    return True


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--once", action="store_true", help="claim at most one task, then exit")
    args = parser.parse_args()
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    try:
        config = WorkerConfig.from_env()
        protocol = load_protocol(config.protocol_root)
        api = WorkerAPI(config)
    except Exception as exc:
        LOGGER.error("worker startup failed: %s", sanitize_error(f"{type(exc).__name__}: {exc}"))
        return 2

    LOGGER.info("worker=%s ready", config.worker_id)
    try:
        while True:
            worked = run_once(api, protocol)
            if args.once:
                return 0
            if not worked:
                time.sleep(config.poll_seconds)
    except KeyboardInterrupt:
        LOGGER.info("worker stopped")
        return 0


if __name__ == "__main__":
    raise SystemExit(main())
