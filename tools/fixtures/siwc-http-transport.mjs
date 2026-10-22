// Offline HTTP boundary for the real pinned protocol runner. No helper mocks.
import assert from 'node:assert/strict';
import fs from 'node:fs';
export const shouldUseTlsTransport = () => true;
export const browserIdentityForTlsProfile = () => ({userAgent:'offline-test',acceptLanguage:'en-US'});
export class TlsFingerprintTransport {
  enabled = true;
  async prepareProxy() {}
  async close() {}
  async generateSentinelTokens() { return {token:'synthetic-sentinel'}; }
  async fetch(url, options) {
    const target = new URL(url);
    assert.equal(target.origin, 'https://auth.openai.com');
    const route = `${options.method} ${target.pathname}`;
    fs.appendFileSync(process.env.SIWC_TEST_ROUTES, route+'\n');
    const mode = process.env.SIWC_TEST_MODE;
    const json = value => Response.json(value);
    const callback = `http://127.0.0.1:1455/auth/callback?code=synthetic-code&state=${mode==='wrong-state'?'wrong':'synthetic-state'}&client_id=issued`;
    if (route==='GET /api/accounts/authorize') return new Response('',{status:302,headers:{location:'/log-in','set-cookie':'oai-did=synthetic-device; Path=/; Secure'}});
    if (route==='GET /log-in' || route==='GET /log-in/password' || route==='GET /consent') return new Response('<html>Offline page</html>',{headers:{'content-type':'text/html'}});
    if (route==='POST /api/accounts/authorize/continue') {
      assert.equal(JSON.parse(options.body).username.value,'test@example.test');
      return json({continue_url:'https://auth.openai.com/log-in/password'});
    }
    if (route==='POST /api/accounts/password/verify') {
      assert.equal(JSON.parse(options.body).password,'synthetic-password');
      if (mode==='password-rejected') return Response.json({error:{message:'invalid_password'}},{status:400});
      return json({page:{type:'mfa_challenge'},'oai-client-auth-session':{mfa_factors:[{factor_type:'totp',id:'test-factor'}]}});
    }
    if (route==='POST /api/accounts/mfa/issue_challenge') return json({});
    if (route==='POST /api/accounts/mfa/verify') {
      assert.match(JSON.parse(options.body).code,/^\d{6}$/);
      return json({page:{type:'workspace'},continue_url:'https://auth.openai.com/workspace','oai-client-auth-session':{workspaces:[{id:'wrong-workspace',kind:'organization'},{id:'expected-workspace',kind:'personal'}]}});
    }
    if (route==='POST /api/accounts/workspace/select') {
      assert.equal(JSON.parse(options.body).workspace_id,'expected-workspace');
      return json({continue_url:mode==='consent'?'https://auth.openai.com/consent':callback});
    }
    throw new Error(`UNEXPECTED_OFFLINE_ROUTE ${route}`);
  }
}
