"""Remote CI only: prove regression behavior with baseline implementations."""
from pathlib import Path
import subprocess

baseline = 'e6b30da8f75b382a20b56b712a209e9f7d8c72fb'
restored = [
    'backend/internal/service/admin_user.go',
    'backend/internal/service/openai_images.go',
    'backend/internal/service/grok_media.go',
    'backend/internal/service/openai_chat_roles.go',
    'backend/internal/service/gateway_billing_block.go',
    'backend/internal/service/openai_codex_models_service.go',
]
mutated = [
    'backend/internal/service/channel_service.go',
    'backend/internal/pkg/antigravity/response_transformer.go',
    'backend/internal/service/antigravity_gateway_streaming.go',
    'backend/internal/service/gemini_messages_compat_service.go',
    'backend/internal/pkg/adminaccess/catalog.go',
    'backend/internal/service/ratelimit_cn_providers.go',
]
expected = [
    'TestAdminService_UpdateUser_AdminGuardsAreClientErrors',
    'TestParseOpenAIImagesRequest_MultipartKeepsUploadAbove20MB',
    'TestParseOpenAIImagesRequest_MultipartRejectsUploadAbovePartLimit',
    'TestParseGrokMediaRequest_MultipartKeepsUploadAbove20MB',
    'TestNormalizeStrictChatDeveloperRoles_OpenCodeModelRouting',
    'TestClaudeCodeFingerprint_JavaScriptUTF16',
    'TestOpenAIModelsSingleflightRechecksCompletedRefresh',
    'TestChannelService_ApplyUpdateInputMergesWebSearchEmulationDimensions',
    'TestMalformedFunctionCallTransform',
    'TestAntigravityMalformedFunctionCallNonStreaming',
    'TestScopedAdminRegisteredRoutesEnforceDefaultDenyAndAccountScope',
    'TestHandleUpstreamError_ZhipuFrequency429UsesShortCooldown',
    'TestGeminiForward_SameAccountRetriesFollowConfig',
    'TestGeminiForwardNative_BoundCacheUpstreamGatewayErrorsRetryablePerPoolModeCodes',
]

def replace(path, old, new):
    p = Path(path)
    source = p.read_text()
    if old not in source:
        raise RuntimeError(f'mutation anchor missing: {path}')
    p.write_text(source.replace(old, new, 1))

try:
    for path in restored:
        Path(path).write_bytes(subprocess.check_output(['git', 'show', f'{baseline}:{path}']))
    replace(mutated[0], 'channel.FeaturesConfig = mergeWebSearchEmulationDimensions(channel.FeaturesConfig, input.FeaturesConfig)', 'channel.FeaturesConfig = input.FeaturesConfig')
    replace(mutated[1], 'if len(v1Resp.Response.Candidates) > 0 && v1Resp.Response.Candidates[0].FinishReason == "MALFORMED_FUNCTION_CALL" {', 'if false {')
    replace(mutated[2], 'if extractGeminiFinishReason(parsed) == "MALFORMED_FUNCTION_CALL" {', 'if false {')
    replace(mutated[3], 'return s.cfg.Gateway.MaxSameAccountRetriesGemini + 1', 'return geminiDefaultSameAccountRetries + 1')
    replace(mutated[3], 'retryable := account.IsPoolMode() && account.IsPoolModeRetryableStatus(statusCode)', 'retryable := false && account.IsPoolMode() && account.IsPoolModeRetryableStatus(statusCode)')
    replace(mutated[5], 'cnProviderResponseIndicatesFrequencyLimit(responseBody) &&', 'false && cnProviderResponseIndicatesFrequencyLimit(responseBody) &&')
    replace(mutated[4], '\n\treturn false\n}', '\n\treturn true // mutation: unknown routes incorrectly inherit access\n}')
    result = subprocess.run([
        'go', 'test', '-tags=unit', './internal/service', './internal/pkg/antigravity', './internal/server/routes',
        '-run', '^(' + '|'.join(expected) + ')$', '-count=1', '-v',
    ], cwd='backend', stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    print(result.stdout)
    if result.returncode == 0 or '[build failed]' in result.stdout:
        raise RuntimeError('expected behavior failures, not a passing or unbuildable suite')
    for name in expected:
        if f'--- FAIL: {name}' not in result.stdout:
            raise RuntimeError(f'missing behavior RED: {name}')
finally:
    subprocess.run(['git', 'restore', '--source=HEAD', '--', *restored, *mutated], check=True)
