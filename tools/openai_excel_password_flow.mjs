import crypto from 'node:crypto';

export async function loginExcelPassword(client, options, helpers) {
  const {authBase, email, password, totpSecret, rl, clientId, redirectUri} = options;
  if (authBase !== 'https://auth.openai.com') throw new Error('EXCEL_AUTH_ORIGIN_INVALID');
  const verifier = crypto.randomBytes(48).toString('base64url');
  const state = `bps.${crypto.randomBytes(24).toString('base64url')}.PC`;
  const query = new URLSearchParams({client_id:clientId, redirect_uri:redirectUri,
    response_type:'code', scope:'openid offline_access email profile organization.read',
    audience:'https://api.openai.com/v1', platform:'PC', state,
    code_challenge_method:'S256', code_challenge:crypto.createHash('sha256').update(verifier).digest('base64url')});
  console.log('[excel] Start official Excel PKCE authorization.');
  let page = await client.follow(`${authBase}/api/accounts/authorize?${query}`);
  const deviceId = client.jar.value('oai-did', authBase) || crypto.randomUUID();
  if (!helpers.isPasswordLoginPage(page.finalUrl)) {
    if (!helpers.isAuthLoginPage(page.finalUrl)) throw new Error('EXCEL_UNEXPECTED_INITIAL_PAGE');
    const headers = await helpers.createSentinelHeaders(client, {authBase, deviceId, flow:'authorize_continue'});
    const {data} = await helpers.authJsonStep(client, authBase, 'POST', '/api/accounts/authorize/continue',
      {username:{kind:'email',value:email}}, {referer:page.finalUrl, headers});
    const next = helpers.getContinueUrl(data);
    if (!next || !helpers.isPasswordLoginPage(next)) throw new Error('EXCEL_ADDITIONAL_VERIFICATION_REQUIRED');
    page = await client.follow(next, {referer:page.finalUrl});
  }
  if (!helpers.isPasswordLoginPage(page.finalUrl)) throw new Error('EXCEL_PASSWORD_PAGE_REQUIRED');
  let payload = await helpers.verifyPassword(client,{authBase, rl, deviceId, password, referer:page.finalUrl});
  const mfaRequired = helpers.isMfaChallengePayload(payload);
  payload = await helpers.completeTotpMfaIfNeeded(client,{authBase,rl,deviceId,payload,totpSecret,referer:page.finalUrl});
  payload = await helpers.selectChatgptLoginWorkspaceIfNeeded(client,{authBase,payload});
  const continued = await helpers.continueFlow(client,payload);
  const callback = new URL(continued?.finalUrl || helpers.getContinueUrl(payload) || authBase);
  const expected = new URL(redirectUri);
  if (callback.origin !== expected.origin || callback.pathname !== expected.pathname ||
      callback.username || callback.password || callback.searchParams.get('state') !== state ||
      !callback.searchParams.get('code')) throw new Error('EXCEL_CALLBACK_STATE_MISMATCH');
  client.excelOAuth = {callbackUrl:callback.toString(),codeVerifier:verifier,state};
  console.log('[excel] Password/TOTP authorization completed.');
  return {deviceId,emailVerified:true,loginMethod:'password',mfaVerified:mfaRequired};
}
