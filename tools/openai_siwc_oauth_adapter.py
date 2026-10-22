"""SIWC callback adapter for the pinned password/TOTP runtime.

The API owns PKCE and validates tokens. Only an account-bound callback leaves
this temporary worker copy; no runtime globals or original dependency change.
"""
import shutil
from pathlib import Path

try:
    from .openai_excel_oauth_adapter import replace_exact
except ImportError:
    from openai_excel_oauth_adapter import replace_exact


def siwc_source(source):
    source = replace_exact(source, 'import crypto from "node:crypto";',
                           'import crypto from "node:crypto";\nimport {loginSIWCPassword} from "./openai_siwc_password_flow.mjs";')
    source = replace_exact(source, 'async function loginChatgptWeb(client, { chatgptBase, authBase, email, rl, password, totpSecret }) {', '''async function loginChatgptWeb(client, { chatgptBase, authBase, email, rl, password, totpSecret }) {
  return loginSIWCPassword(client,{authBase,email,rl,password,totpSecret},
    {isPasswordLoginPage,isAuthLoginPage,createSentinelHeaders,authJsonStep,getContinueUrl,verifyPassword,
     isMfaChallengePayload,completeTotpMfaIfNeeded,selectChatgptLoginWorkspaceIfNeeded,continueFlow});''')
    source = replace_exact(source, 'async function runCodexOauth(client, options) {', '''async function runCodexOauth(client, options) {
  if (client.siwcOAuth) return client.siwcOAuth;
  throw new Error("SIWC_CALLBACK_MISSING");''')
    source = replace_exact(source, '      console.log(codex.callbackUrl);', '      console.log("[siwc] callback received; query withheld");')
    source = replace_exact(source, '    if (codex?.callbackUrl && codex?.codeVerifier && !args.noSub2apiExport', '''    if (codex?.callbackUrl) {
      await writeJsonAtomic(sub2apiOutPath, {siwc_callback_url:codex.callbackUrl});
      await removeProtocolCheckpoint(checkpointPath);
      return;
    }
    if (codex?.callbackUrl && codex?.codeVerifier && !args.noSub2apiExport''')
    source = replace_exact(source, '    maxProxySessionAttempts: process.env.CHATGPT_PROXY_MAX_ATTEMPTS || 10,',
                           '    maxProxySessionAttempts: 1,\n    sameProxyRiskRetries: 0,\n    cloudflareSolver: false,')
    source = replace_exact(source, 'await transport.prepareProxy(proxyTemplate, `${chatgptBase}/`);',
                           'await transport.prepareProxy(proxyTemplate, `${authBase}/`);', count=3)
    source = replace_exact(source, 'async function ask(rl, prompt) {',
                           'async function ask(rl, prompt) {\n  throw new Error("SIWC_INTERACTION_REQUIRED");')
    source = replace_exact(source, '  const organization = workspaces.find((item) => item?.kind === "organization" && item?.id);', '''  const expected = process.env.OPENAI_SIWC_EXPECTED_WORKSPACE;
  if (expected) {
    const selected = workspaces.find(item => item?.id === expected);
    if (!selected) throw new Error("SIWC_WORKSPACE_MISMATCH");
    return selected.id;
  }
  const organization = workspaces.find((item) => item?.kind === "organization" && item?.id);''')
    return source


def prepare_siwc_runtime(root: Path, temporary: Path) -> Path:
    staged = temporary / 'siwc-runtime'
    patched = siwc_source((root / 'src/protocol-login.mjs').read_text())
    shutil.copytree(root / 'src', staged / 'src')
    if (root / 'node_modules').is_dir():
        (staged / 'node_modules').symlink_to((root / 'node_modules').resolve(), target_is_directory=True)
    script = staged / 'src/protocol-login.mjs'
    script.write_text(patched)
    shutil.copy2(Path(__file__).with_name('openai_siwc_password_flow.mjs'), script.parent)
    return script
