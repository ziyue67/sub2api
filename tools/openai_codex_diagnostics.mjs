// Private, bounded diagnostic snapshots. Never serialize exception text or URLs.
import fs from 'node:fs';

const stages = new Set(['starting', 'web_login', 'password_accepted', 'mfa_challenge',
  'mfa_accepted', 'web_redirect', 'oauth_authorize', 'session_select',
  'workspace_select', 'callback_validation', 'token_exchange', 'oauth_output', 'completed']);
const reasons = new Set(['none', 'protocol_error', 'password_rejected', 'totp_rejected',
  'interaction_required', 'security_check', 'session_unavailable', 'workspace_mismatch',
  'callback_missing', 'callback_invalid', 'callback_state_mismatch', 'pkce_invalid',
  'token_exchange_failed', 'credentials_incomplete', 'transport_error', 'runtime_error']);

function reasonFor(error, stage) {
  try {
    if (reasons.has(error?.message)) return error.message;
    const text = typeof error?.message === 'string' ? error.message : '';
    if (/PROXY_RISK_CONTROL|security-check|security check/i.test(text)) return 'security_check';
    if (/CODEX_AUTH_LOGIN_REQUIRED|SESSION_SELECTION_INVALID/.test(text)) return 'session_unavailable';
    if (/ECONNREFUSED|ENOTFOUND|ETIMEDOUT|fetch failed|timed out/.test(text)) return 'transport_error';
    if (/ERR_MODULE_NOT_FOUND|MODULE_NOT_FOUND|ENOENT/.test(text)) return 'runtime_error';
    if (stage === 'token_exchange') return 'token_exchange_failed';
  } catch { /* Even a hostile exception getter cannot enter the channel. */ }
  return 'protocol_error';
}

export function createProtocolDiagnostics(file, taskId) {
  const started = performance.now();
  let stage = 'starting';
  function write(reason) {
    if (typeof file !== 'string' || !Number.isSafeInteger(taskId) || taskId <= 0) return;
    const value = {version: 1, task_id: taskId, stage, reason,
      elapsed_ms: Math.min(3600000, Math.max(0, Math.floor(performance.now() - started)))};
    try {
      fs.writeFileSync(file + '.tmp', JSON.stringify(value), {mode: 0o600});
      fs.renameSync(file + '.tmp', file);
    } catch { /* Optional diagnostics must not change an authentication result. */ }
  }
  write('none');
  return {
    mark(next) {
      if (!stages.has(next)) return;
      stage = next;
      write('none');
    },
    fail(error) { write(reasonFor(error, stage)); },
  };
}

export function validateCodexCallback(codex, redirectUri) {
  if (!codex || typeof codex.callbackUrl !== 'string' || !codex.callbackUrl) {
    throw new Error('callback_missing');
  }
  let callback, expected;
  try { callback = new URL(codex.callbackUrl); expected = new URL(redirectUri); }
  catch { throw new Error('callback_invalid'); }
  if (callback.origin !== expected.origin || callback.pathname !== expected.pathname ||
      callback.username || callback.password || callback.hash) throw new Error('callback_invalid');
  if (callback.searchParams.has('error')) throw new Error('protocol_error');
  if (typeof codex.state !== 'string' || !codex.state ||
      callback.searchParams.getAll('state').length !== 1 ||
      callback.searchParams.get('state') !== codex.state) throw new Error('callback_state_mismatch');
  if (callback.searchParams.getAll('code').length !== 1 ||
      !callback.searchParams.get('code')?.trim()) throw new Error('callback_missing');
  if (typeof codex.codeVerifier !== 'string' || !/^[A-Za-z0-9._~-]{43,128}$/.test(codex.codeVerifier)) {
    throw new Error('pkce_invalid');
  }
}
