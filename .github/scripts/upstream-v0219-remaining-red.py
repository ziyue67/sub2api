"""Remote-only mutations prove fixed-tag pricing/catalog regression sensitivity."""
from pathlib import Path
import subprocess

changes = {
    'backend/internal/service/billing_service.go': [('if isDeepSeekProModel(model) {', 'if false && isDeepSeekProModel(model) {')],
    'backend/internal/service/openai_gateway_response_handling.go': [('value.Get("prompt_cache_hit_tokens"), // DeepSeek：prompt_tokens = hit + miss', '// mutation: missing DeepSeek cache bucket')],
    'backend/internal/service/gemini_image_output_accounting.go': [('{"inlineData", "inline_data", "fileData", "file_data"}', '{"inlineData", "inline_data"}')],
    'backend/internal/service/model_plaza_service.go': [('if groupPricing.BillingMode == BillingModeImage || groupPricing.BillingMode == BillingModePerRequest {', 'if false {')],
    'backend/internal/service/image_billing_count.go': [('return reported', 'return 1 // mutation: ignore returned image count')],
    'backend/internal/service/openai_gateway_grok.go': [('UpstreamResponseModel:         observedUpstreamResponseModel(c),', 'UpstreamResponseModel:         "",')],
    'backend/internal/repository/account_repo_grok_models.go': [('if affected == 0 {', 'affected = 1 // mutation: report lost CAS as saved\n\tif affected == 0 {')],
}
expected = [
    'TestCalculateCostUnified_DeepseekProRetainsRatesAfterSeptember14',
    'TestOpenAIUsageDeepSeekPromptCacheHitTokensCountAsCacheRead',
    'TestCountGeminiImageOutputs',
    'TestListPlazaGroups_GroupModelImagePricingWinsOverGroupImageTiers',
    'TestResolveBillableImageCount',
    'TestGrokResponseAuditNativeJSON',
    'TestGrokCatalogCASWritesSingleKeyAndReportsRows',
]
try:
    for path, replacements in changes.items():
        p = Path(path)
        source = p.read_text()
        for old, new in replacements:
            if old not in source:
                raise RuntimeError(f'missing mutation anchor: {path}: {old}')
            source = source.replace(old, new, 1)
        p.write_text(source)
    result = subprocess.run([
        'go', 'test', '-tags=unit', './internal/service', './internal/repository',
        '-run', '^(' + '|'.join(expected) + ')$', '-count=1', '-v',
    ], cwd='backend', text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    print(result.stdout)
    if result.returncode == 0 or '[build failed]' in result.stdout:
        raise RuntimeError('expected behavior failures, never compilation failures')
    for name in expected:
        if f'--- FAIL: {name}' not in result.stdout:
            raise RuntimeError(f'missing RED: {name}')
finally:
    subprocess.run(['git', 'restore', '--source=HEAD', '--', *changes], check=True)
