// SIWC runs the existing password/TOTP helpers against a server-bound OAuth URL.
// New consent or additional verification is handed back to the administrator.
import crypto from 'node:crypto';

export async function loginSIWCPassword(client, options, helpers) {
  const {authBase, email, password, totpSecret, rl} = options;
  const auth = new URL(process.env.OPENAI_SIWC_AUTH_URL || 'about:blank');
  if (authBase !== 'https://auth.openai.com' || auth.origin !== authBase ||
      auth.pathname !== '/api/accounts/authorize' ||
      auth.searchParams.get('redirect_uri') !== 'http://127.0.0.1:1455/auth/callback' ||
      !auth.searchParams.get('state') || !auth.searchParams.get('nonce') ||
      !auth.searchParams.get('scope')?.split(' ').includes('chatgpt.tokens.use.direct')) {
    throw new Error('SIWC_AUTH_URL_INVALID');
  }
  const state = auth.searchParams.get('state');
  function completed(url) {
    let callback;
    try { callback = new URL(url); } catch { return false; }
    if (callback.origin !== 'http://127.0.0.1:1455' || callback.pathname !== '/auth/callback') return false;
    if (callback.username || callback.password || callback.hash || callback.searchParams.get('state') !== state ||
        !callback.searchParams.get('code') || callback.searchParams.has('error')) throw new Error('SIWC_CALLBACK_INVALID');
    client.siwcOAuth = {callbackUrl: callback.toString(), state, codeVerifier: 'server-owned'};
    return true;
  }
  let page = await client.follow(auth.toString());
  if (completed(page.finalUrl)) return {emailVerified: true};
  const deviceId = client.jar.value('oai-did', authBase) || crypto.randomUUID();
  if (!helpers.isPasswordLoginPage(page.finalUrl)) {
    if (!helpers.isAuthLoginPage(page.finalUrl)) throw new Error('SIWC_INTERACTION_REQUIRED');
    const headers = await helpers.createSentinelHeaders(client, {authBase, deviceId, flow: 'authorize_continue'});
    const {data} = await helpers.authJsonStep(client, authBase, 'POST', '/api/accounts/authorize/continue',
      {username: {kind: 'email', value: email}}, {referer: page.finalUrl, headers});
    const next = helpers.getContinueUrl(data);
    if (!next || !helpers.isPasswordLoginPage(next)) throw new Error('SIWC_INTERACTION_REQUIRED');
    page = await client.follow(next, {referer: page.finalUrl});
  }
  let payload = await helpers.verifyPassword(client, {authBase, rl, deviceId, password, referer: page.finalUrl});
  const mfaRequired = helpers.isMfaChallengePayload(payload);
  payload = await helpers.completeTotpMfaIfNeeded(client, {authBase, rl, deviceId, payload, totpSecret, referer: page.finalUrl});
  payload = await helpers.selectChatgptLoginWorkspaceIfNeeded(client, {authBase, payload});
  const continued = await helpers.continueFlow(client, payload);
  if (!completed(continued?.finalUrl || helpers.getContinueUrl(payload))) throw new Error('SIWC_INTERACTION_REQUIRED');
  return {deviceId, emailVerified: true, loginMethod: 'password', mfaVerified: mfaRequired};
}
