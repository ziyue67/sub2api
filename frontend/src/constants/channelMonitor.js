/**
 * Channel monitor shared constants.
 *
 * Single source of truth for provider/status string values used by both the
 * admin (`views/admin/ChannelMonitorView.vue`) and user-facing
 * (`views/user/ChannelStatusView.vue`) screens, plus the shared composable
 * `useChannelMonitorFormat`.
 */
export const PROVIDER_OPENAI = 'openai';
export const PROVIDER_ANTHROPIC = 'anthropic';
export const PROVIDER_GEMINI = 'gemini';
export const PROVIDER_GROK = 'grok';
export const PROVIDER_ANTIGRAVITY = 'antigravity';
export const PROVIDER_KIMI = 'kimi';
export const PROVIDER_ZHIPU = 'zhipu';
export const PROVIDER_DEEPSEEK = 'deepseek';
export const PROVIDER_MINIMAX = 'minimax';
export const PROVIDER_OPENCODE_GO = 'opencode_go';
export const DEFAULT_GROK_ENDPOINT = 'https://api.x.ai';
export const DEFAULT_GROK_MODEL = 'grok-4.5';
/** 国产 provider 的官方 endpoint（探活模式预填；配额模式可留空）。 */
export const DEFAULT_KIMI_ENDPOINT = 'https://api.moonshot.cn';
export const DEFAULT_ZHIPU_ENDPOINT = 'https://open.bigmodel.cn';
export const DEFAULT_DEEPSEEK_ENDPOINT = 'https://api.deepseek.com';
export const DEFAULT_MINIMAX_ENDPOINT = 'https://api.minimaxi.com';
export const DEFAULT_OPENCODE_GO_ENDPOINT = 'https://opencode.ai/zen/go/v1';
export const CHECK_MODE_PROBE = 'probe';
export const CHECK_MODE_QUOTA = 'quota';
export const CHECK_MODE_QUOTA_PROBE = 'quota_probe';
export const API_MODE_CHAT_COMPLETIONS = 'chat_completions';
export const API_MODE_RESPONSES = 'responses';
export const PROVIDERS = [
    PROVIDER_OPENAI,
    PROVIDER_ANTHROPIC,
    PROVIDER_GEMINI,
    PROVIDER_GROK,
    PROVIDER_ANTIGRAVITY,
    PROVIDER_KIMI,
    PROVIDER_ZHIPU,
    PROVIDER_DEEPSEEK,
    PROVIDER_MINIMAX,
    PROVIDER_OPENCODE_GO,
];
/** 仅支持配额模式（无探活 adapter）的 provider。 */
export const QUOTA_ONLY_PROVIDERS = [PROVIDER_ANTIGRAVITY];
export const CHECK_MODES = [
    CHECK_MODE_PROBE,
    CHECK_MODE_QUOTA,
    CHECK_MODE_QUOTA_PROBE,
];
export const API_MODES = [
    API_MODE_CHAT_COMPLETIONS,
    API_MODE_RESPONSES,
];
export const STATUS_OPERATIONAL = 'operational';
export const STATUS_DEGRADED = 'degraded';
export const STATUS_FAILED = 'failed';
export const STATUS_ERROR = 'error';
export const MONITOR_STATUSES = [
    STATUS_OPERATIONAL,
    STATUS_DEGRADED,
    STATUS_FAILED,
    STATUS_ERROR,
];
/** Default polling interval (seconds) for new monitors. */
export const DEFAULT_INTERVAL_SECONDS = 60;
