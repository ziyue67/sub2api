import assert from 'node:assert/strict';
import test from 'node:test';
import {loginSIWCPassword} from './openai_siwc_password_flow.mjs';

function fixture(continued = 'success') {
  const auth = new URL('https://auth.openai.com/api/accounts/authorize');
  for (const [key, value] of Object.entries({state:'attempt',nonce:'nonce',redirect_uri:'http://127.0.0.1:1455/auth/callback',scope:'openid chatgpt.tokens.use.direct'})) auth.searchParams.set(key,value);
  process.env.OPENAI_SIWC_AUTH_URL = auth.toString();
  const calls = [];
  const client = {jar:{value:()=> 'device'},follow:async url=>({finalUrl:url.includes('/authorize?')?'https://auth.openai.com/log-in':url})};
  const options = {authBase:'https://auth.openai.com',email:'test@example.test',password:'synthetic-password',totpSecret:'synthetic-totp'};
  const helpers = {
    isPasswordLoginPage:url=>url.endsWith('/log-in/password'),isAuthLoginPage:url=>url.endsWith('/log-in'),
    getContinueUrl:data=>data?.continue_url,createSentinelHeaders:async()=>({}),
    authJsonStep:async()=>({data:{continue_url:'https://auth.openai.com/log-in/password'}}),
    verifyPassword:async(_client,args)=>{assert.equal(args.password,options.password);calls.push('password');return {mfa:true};},
    isMfaChallengePayload:()=>true,
    completeTotpMfaIfNeeded:async(_client,args)=>{assert.equal(args.totpSecret,options.totpSecret);calls.push('totp');return {};},
    selectChatgptLoginWorkspaceIfNeeded:async()=>({}),
    continueFlow:async()=>({finalUrl:continued==='success'?'http://127.0.0.1:1455/auth/callback?code=code&state=attempt&client_id=issued':continued}),
  };
  return {client,options,helpers,calls};
}

test('saved password and TOTP produce only a server-bound callback',async()=>{
  const f=fixture();const result=await loginSIWCPassword(f.client,f.options,f.helpers);
  assert.equal(result.mfaVerified,true);assert.deepEqual(f.calls,['password','totp']);
  assert.equal(new URL(f.client.siwcOAuth.callbackUrl).searchParams.get('client_id'),'issued');
  assert.equal(f.client.siwcOAuth.codeVerifier,'server-owned');
  assert.equal(f.client.siwcOAuth.access_token,undefined);
});
test('new consent is handed off rather than silently changed',async()=>{
  const f=fixture('https://auth.openai.com/consent');
  await assert.rejects(loginSIWCPassword(f.client,f.options,f.helpers),/SIWC_INTERACTION_REQUIRED/);
  assert.equal(f.client.siwcOAuth,undefined);
});
test('wrong callback state and untrusted authorization origin are rejected',async()=>{
  const f=fixture('http://127.0.0.1:1455/auth/callback?code=code&state=another&client_id=issued');
  await assert.rejects(loginSIWCPassword(f.client,f.options,f.helpers),/SIWC_CALLBACK_INVALID/);
  process.env.OPENAI_SIWC_AUTH_URL='https://untrusted.example/authorize';
  await assert.rejects(loginSIWCPassword(f.client,f.options,f.helpers),/SIWC_AUTH_URL_INVALID/);
  delete process.env.OPENAI_SIWC_AUTH_URL;
});
