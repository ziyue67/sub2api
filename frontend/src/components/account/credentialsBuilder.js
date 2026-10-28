import { openAIPlanTypeKey, openAIPlanTypeLabel, openAIPlanTypes } from '@/utils/planType';
import { BUILTIN_PLATFORM_CATALOG, getProviderMode, getProviderProfile } from '@/constants/platformCatalog';
export function applyInterceptWarmup(credentials, enabled, mode) {
    if (enabled) {
        credentials.intercept_warmup_requests = true;
    }
    else if (mode === 'edit') {
        delete credentials.intercept_warmup_requests;
    }
}
export const ANTIGRAVITY_PROJECT_ID_CREDENTIAL_KEY = 'antigravity_project_id';
export function applyAntigravityProjectID(credentials, projectId, mode) {
    const trimmed = projectId.trim();
    if (trimmed) {
        credentials[ANTIGRAVITY_PROJECT_ID_CREDENTIAL_KEY] = trimmed;
    }
    else if (mode === 'edit') {
        delete credentials[ANTIGRAVITY_PROJECT_ID_CREDENTIAL_KEY];
    }
}
// ========== 请求头覆写（API-key 平台 + grok 的 api_key/oauth 账号） ==========
export const HEADER_OVERRIDE_ENABLED_CREDENTIAL_KEY = 'header_override_enabled';
export const HEADER_OVERRIDES_CREDENTIAL_KEY = 'header_overrides';
/** 请求头覆写资格（与后端 IsHeaderOverrideEligible 保持一致） */
export function isHeaderOverrideCapable(platform, type) {
    if (platform === 'anthropic' || platform === 'openai' || isMultiProtocolApiKeyPlatform(platform)) {
        return type === 'apikey';
    }
    if (platform === 'grok') {
        return type === 'apikey' || type === 'oauth';
    }
    return false;
}
/** 禁止覆写的请求头（与后端 headerOverrideBlockedNames 保持一致） */
const HEADER_OVERRIDE_BLOCKED_NAMES = new Set([
    'host',
    'content-length',
    'content-type',
    'transfer-encoding',
    'connection',
    'keep-alive',
    'proxy-authenticate',
    'proxy-authorization',
    'proxy-connection',
    'te',
    'trailer',
    'upgrade',
    'authorization',
    'x-api-key',
    'x-goog-api-key',
    'cookie',
    'accept-encoding',
    'sec-websocket-key',
    'sec-websocket-version',
    'sec-websocket-extensions',
    'sec-websocket-protocol',
    'sec-websocket-accept',
    'session_id',
    'conversation_id',
    'x-codex-turn-state',
    'x-codex-turn-metadata',
    'chatgpt-account-id',
    'x-claude-code-session-id',
    'x-client-request-id',
    'x-grok-conv-id'
]);
/** RFC 7230 token：合法的 HTTP header 名称字符集 */
const HEADER_NAME_PATTERN = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;
function isValidHeaderOverrideName(name) {
    return HEADER_NAME_PATTERN.test(name);
}
/** 与后端 maxHeaderOverride* 常量保持一致 */
const HEADER_OVERRIDE_MAX_ENTRIES = 64;
const HEADER_OVERRIDE_MAX_NAME_LENGTH = 200;
const HEADER_OVERRIDE_MAX_VALUE_LENGTH = 8192;
/** header value 不允许包含控制字符（与后端 httpguts.ValidHeaderFieldValue 对齐） */
// eslint-disable-next-line no-control-regex
const HEADER_VALUE_INVALID_PATTERN = /[\x00-\x08\x0a-\x1f\x7f]/;
/** 长度限制按 UTF-8 字节计（与后端 Go len() 对齐，避免多字节值前端放行后端 400） */
const HEADER_TEXT_ENCODER = new TextEncoder();
function utf8ByteLength(value) {
    return HEADER_TEXT_ENCODER.encode(value).length;
}
/**
 * 校验请求头覆写行，返回首个错误的 i18n key（无错误返回 null）。
 * 名称为空但值非空 → invalidName；名称非法 → invalidName；
 * 禁止覆写 → blockedName；大小写不敏感重名 → duplicateName；
 * 值含控制字符或超长 → invalidValue；条目过多 → tooManyEntries。
 */
export function validateHeaderOverrideRows(rows) {
    const seen = new Set();
    for (const row of rows) {
        const name = row.name.trim();
        const value = row.value.trim();
        if (!name) {
            if (value)
                return 'invalidName';
            continue;
        }
        if (!isValidHeaderOverrideName(name) || name.length > HEADER_OVERRIDE_MAX_NAME_LENGTH) {
            return 'invalidName';
        }
        const lower = name.toLowerCase();
        if (HEADER_OVERRIDE_BLOCKED_NAMES.has(lower))
            return 'blockedName';
        if (seen.has(lower))
            return 'duplicateName';
        if (HEADER_VALUE_INVALID_PATTERN.test(value) ||
            utf8ByteLength(value) > HEADER_OVERRIDE_MAX_VALUE_LENGTH) {
            return 'invalidValue';
        }
        seen.add(lower);
    }
    if (seen.size > HEADER_OVERRIDE_MAX_ENTRIES)
        return 'tooManyEntries';
    return null;
}
/** 行数组 → credentials 存储对象（名称小写化，丢弃空行） */
export function buildHeaderOverridesObject(rows) {
    const result = {};
    for (const row of rows) {
        const name = row.name.trim().toLowerCase();
        if (!name)
            continue;
        result[name] = row.value.trim();
    }
    return result;
}
/** credentials 存储对象 → 行数组（按名称排序保证稳定展示） */
export function splitHeaderOverridesObject(record) {
    if (!record || typeof record !== 'object' || Array.isArray(record))
        return [];
    return Object.entries(record)
        .filter(([, value]) => typeof value === 'string')
        .map(([name, value]) => ({ name, value: value }))
        .sort((a, b) => a.name.localeCompare(b.name));
}
/**
 * 解析粘贴的 JSON 文本为请求头覆写行。
 * 仅接受扁平 JSON 对象；值允许 string/number/boolean（统一转字符串），
 * 其余类型或非对象输入返回 null 表示格式非法。键为空白的条目直接丢弃。
 */
export function parseHeaderOverridesJson(text) {
    let parsed;
    try {
        parsed = JSON.parse(text);
    }
    catch {
        return null;
    }
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed))
        return null;
    const rows = [];
    for (const [rawName, rawValue] of Object.entries(parsed)) {
        const name = rawName.trim();
        if (!name)
            continue;
        if (typeof rawValue !== 'string' &&
            typeof rawValue !== 'number' &&
            typeof rawValue !== 'boolean') {
            return null;
        }
        rows.push({ name, value: String(rawValue).trim() });
    }
    return rows.sort((a, b) => a.name.localeCompare(b.name));
}
/** 请求头覆写行 → 便于迁移/备份的 JSON 文本（跳过名称为空的占位行） */
export function serializeHeaderOverrideRows(rows) {
    const record = {};
    for (const row of rows) {
        const name = row.name.trim();
        if (!name)
            continue;
        record[name] = row.value.trim();
    }
    return JSON.stringify(record, null, 2);
}
// ========== Grok 自定义转发地址（base_url 仅改写转发端点，凭证生命周期不受影响） ==========
/** OAuth 账号建号/刷新默认写入的 CLI 网关 host——只有它视同"未定制"。 */
const GROK_DEFAULT_GATEWAY_HOST = 'cli-chat-proxy.grok.com';
/**
 * 判断 Grok 账号存储的 base_url 是否为主动指定的上游端点。
 * 运营方可在官方 API / 区域 API / 第三方转发地址之间手动切换（应对单端点
 * 不可用），这些值都必须回显（开关开启 + 显示地址）。仅默认 CLI 网关
 * （建号/刷新自动写入）、空值与无法解析的值视为"未定制"（与后端
 * GetGrokBaseURL 的回落语义对齐），用于 OAuth 账号编辑时决定开关初始状态。
 */
export function isCustomGrokBaseUrl(value) {
    if (typeof value !== 'string')
        return false;
    const trimmed = value.trim();
    if (!trimmed)
        return false;
    let parsed;
    try {
        parsed = new URL(trimmed);
    }
    catch {
        return false;
    }
    return parsed.hostname.toLowerCase() !== GROK_DEFAULT_GATEWAY_HOST;
}
/**
 * Grok 快捷端点（仅供快速填充，输入框仍可自由填写任意转发地址）。
 * 官方端点偶发不可用时，运营方靠这组预设在端点间手动切换。
 */
export const GROK_BASE_URL_PRESETS = [
    { labelKey: 'cli', url: 'https://cli-chat-proxy.grok.com/v1' },
    { labelKey: 'official', url: 'https://api.x.ai/v1' },
    { label: 'us-east-1', url: 'https://us-east-1.api.x.ai/v1' },
    { label: 'us-west-2', url: 'https://us-west-2.api.x.ai/v1' },
    { label: 'eu-west-1', url: 'https://eu-west-1.api.x.ai/v1' }
];
export function defaultOpenAIAdaptiveBaseUrls() {
    return {
        chat_completions: 'https://api.openai.com',
        anthropic: '',
        responses: ''
    };
}
const NATIVE_API_PROTOCOLS = ['chat_completions', 'anthropic', 'responses'];
export function isCNProviderPlatform(platform) {
    return platform === 'kimi' || platform === 'zhipu' || platform === 'deepseek' || platform === 'minimax';
}
/** 平台是否为多协议 API Key 供应商（平台清单中带 profile）。 */
export function isMultiProtocolApiKeyPlatform(platform) {
    return !!getProviderProfile(platform);
}
/** 供应商在该接入模式下是否提供某协议的原生端点（未知模式按默认模式）。 */
export function providerSupportsProtocol(platform, mode, protocol) {
    return !!getProviderMode(platform, mode)?.base_urls[protocol];
}
/** 供应商是否提供原生 Responses 端点（DeepSeek / Kimi / MiniMax / OpenCode 等）。 */
export function cnSupportsNativeResponses(platform, mode) {
    return providerSupportsProtocol(platform, mode, 'responses');
}
/** 该接入模式下可直连的原生协议（按 chat_completions、anthropic、responses 顺序）。 */
export function providerNativeProtocols(platform, mode) {
    return NATIVE_API_PROTOCOLS.filter(protocol => providerSupportsProtocol(platform, mode, protocol));
}
/** 供应商按模型名选择上游协议（多模型聚合平台），此时 adaptive 账号携带分流规则。 */
export function providerRoutesByModel(platform) {
    return getProviderProfile(platform)?.routing === 'by_model';
}
/** 上游模型目录给出每个模型支持的协议；未命中分流规则的模型据此选协议。 */
export function providerHasModelCatalog(platform) {
    return getProviderProfile(platform)?.model_catalog === true;
}
/** 接入模式的展示名；未知模式原样显示。 */
export function providerModeLabel(mode, t) {
    switch (mode) {
        case 'payg':
            return t('admin.accounts.cnProviders.accountMode.payg');
        case 'coding':
            return t('admin.accounts.cnProviders.accountMode.coding');
        case 'zen':
            return t('admin.accounts.opencodeGo.accountMode.zen');
        case 'go':
            return t('admin.accounts.opencodeGo.accountMode.go');
        default:
            return mode;
    }
}
/** 供应商的接入模式，默认模式在前。 */
export function providerAccountModes(platform) {
    return getProviderProfile(platform)?.modes.map(item => item.mode) ?? [];
}
/** 规范化接入模式：不在该供应商模式列表中的值回落默认模式。 */
export function resolveProviderAccountMode(platform, value) {
    const profile = getProviderProfile(platform);
    if (!profile)
        return typeof value === 'string' ? value : '';
    if (typeof value === 'string' && profile.modes.some(item => item.mode === value))
        return value;
    return profile.default_mode;
}
function builtinModeBaseUrl(platform, mode, protocol) {
    const profile = BUILTIN_PLATFORM_CATALOG.platforms.find(spec => spec.id === platform)?.multi_protocol;
    return profile?.modes.find(item => item.mode === mode)?.base_urls[protocol] ?? '';
}
export const OPENCODE_GO_BASE_URL = builtinModeBaseUrl('opencode_go', 'go', 'chat_completions');
export const OPENCODE_GO_ANTHROPIC_BASE_URL = builtinModeBaseUrl('opencode_go', 'go', 'anthropic');
export const OPENCODE_ZEN_BASE_URL = builtinModeBaseUrl('opencode_go', 'zen', 'chat_completions');
export const OPENCODE_ZEN_ANTHROPIC_BASE_URL = builtinModeBaseUrl('opencode_go', 'zen', 'anthropic');
export function isOpenCodeGoPlatform(platform) {
    return platform === 'opencode_go';
}
export const OPENCODE_GO_PROTOCOL_RULES_KEY = 'protocol_rules';
function isNativeOpenCodeGoProtocol(value) {
    return value === 'chat_completions' || value === 'anthropic' || value === 'responses';
}
/** 从 protocols 列表中取出首选协议以外的原生协议（去重、保持顺序）。 */
function extraRuleProtocols(protocol, protocols) {
    if (!Array.isArray(protocols))
        return [];
    const extra = [];
    for (const item of protocols) {
        if (isNativeOpenCodeGoProtocol(item) && item !== protocol && !extra.includes(item))
            extra.push(item);
    }
    return extra;
}
function buildRule(pattern, protocol, protocols) {
    const extra = extraRuleProtocols(protocol, protocols);
    return extra.length > 0 ? { pattern, protocol, extraProtocols: extra } : { pattern, protocol };
}
/** 规则支持的协议集合，首项为首选协议。 */
export function protocolRuleSet(rule) {
    return [rule.protocol, ...extraRuleProtocols(rule.protocol, rule.extraProtocols)];
}
function normalizeProfileRules(rules) {
    return (rules ?? [])
        .filter(rule => rule.pattern && isNativeOpenCodeGoProtocol(rule.protocol))
        .map(rule => buildRule(rule.pattern, rule.protocol, rule.protocols));
}
function builtinProtocolRules(platform, mode) {
    const profile = BUILTIN_PLATFORM_CATALOG.platforms.find(spec => spec.id === platform)?.multi_protocol;
    return normalizeProfileRules(profile?.modes.find(item => item.mode === mode)?.protocol_rules);
}
export const DEFAULT_OPENCODE_GO_PROTOCOL_RULES = builtinProtocolRules('opencode_go', 'go');
export const DEFAULT_OPENCODE_ZEN_PROTOCOL_RULES = builtinProtocolRules('opencode_go', 'zen');
/** 供应商该接入模式的内置分流规则（首条命中生效；未知模式按默认模式）。 */
export function defaultProviderProtocolRules(platform, mode) {
    return normalizeProfileRules(getProviderMode(platform, mode)?.protocol_rules);
}
export function resolveOpenCodeAccountMode(value) {
    return value === 'zen' ? 'zen' : 'go';
}
export function defaultOpenCodeProtocolRules(mode = 'go') {
    return defaultProviderProtocolRules('opencode_go', mode === 'zen' ? 'zen' : 'go');
}
export function cloneOpenCodeGoProtocolRules(rules = DEFAULT_OPENCODE_GO_PROTOCOL_RULES) {
    return rules.map(rule => buildRule(rule.pattern, rule.protocol, rule.extraProtocols));
}
export function parseOpenCodeGoProtocolRules(raw) {
    if (raw == null)
        return null;
    if (!Array.isArray(raw))
        return cloneOpenCodeGoProtocolRules();
    const rules = [];
    for (const item of raw) {
        if (!item || typeof item !== 'object')
            continue;
        const pattern = typeof item.pattern === 'string'
            ? item.pattern.trim()
            : '';
        const protocols = item.protocols;
        let protocol = item.protocol;
        // 与后端一致：只给了 protocols 时首项即首选协议。
        if ((protocol == null || protocol === '') && Array.isArray(protocols))
            protocol = protocols[0];
        if (!pattern || !isNativeOpenCodeGoProtocol(protocol))
            continue;
        rules.push(buildRule(pattern, protocol, protocols));
    }
    return rules;
}
export function applyOpenCodeGoProtocolRules(credentials, rules, mode) {
    const serialized = rules
        .filter(rule => isNativeOpenCodeGoProtocol(rule.protocol))
        .map(rule => {
        const entry = {
            pattern: rule.pattern.trim().toLowerCase(),
            protocol: rule.protocol
        };
        const protocols = protocolRuleSet(rule);
        if (protocols.length > 1)
            entry.protocols = protocols;
        return entry;
    })
        .filter(rule => rule.pattern.length > 0);
    if (serialized.length > 0 || mode === 'edit') {
        credentials[OPENCODE_GO_PROTOCOL_RULES_KEY] = serialized;
    }
}
/** 各供应商按账号类型 × API 协议分档的快捷端点（点击快速填充，输入框仍可自由填写）。 */
export const CN_BASE_URL_PRESETS = {
    kimi: [
        { mode: 'payg', protocol: 'chat_completions', label: 'Moonshot', url: 'https://api.moonshot.cn/v1' },
        { mode: 'payg', protocol: 'anthropic', label: 'Moonshot Anthropic', url: 'https://api.moonshot.cn/anthropic' },
        { mode: 'payg', protocol: 'responses', label: 'Moonshot Responses', url: 'https://api.moonshot.cn/v1' },
        { mode: 'coding', protocol: 'chat_completions', label: 'Kimi For Coding', url: 'https://api.kimi.com/coding/v1' },
        { mode: 'coding', protocol: 'anthropic', label: 'Kimi Coding Anthropic', url: 'https://api.kimi.com/coding' },
        { mode: 'coding', protocol: 'responses', label: 'Kimi Coding Responses', url: 'https://api.kimi.com/coding/v1' }
    ],
    zhipu: [
        { mode: 'payg', protocol: 'chat_completions', label: 'GLM PaaS', url: 'https://open.bigmodel.cn/api/paas/v4' },
        { mode: 'payg', protocol: 'anthropic', label: 'GLM Anthropic', url: 'https://open.bigmodel.cn/api/anthropic' },
        { mode: 'coding', protocol: 'chat_completions', label: 'GLM Coding', url: 'https://open.bigmodel.cn/api/coding/paas/v4' },
        { mode: 'coding', protocol: 'anthropic', label: 'GLM Coding Anthropic', url: 'https://open.bigmodel.cn/api/anthropic' }
    ],
    deepseek: [
        { mode: 'payg', protocol: 'chat_completions', label: 'DeepSeek', url: 'https://api.deepseek.com' },
        { mode: 'payg', protocol: 'anthropic', label: 'DeepSeek Anthropic', url: 'https://api.deepseek.com/anthropic' },
        { mode: 'payg', protocol: 'responses', label: 'DeepSeek Responses', url: 'https://api.deepseek.com' }
    ],
    minimax: [
        { mode: 'payg', protocol: 'chat_completions', label: 'MiniMax CN', url: 'https://api.minimaxi.com/v1' },
        { mode: 'payg', protocol: 'anthropic', label: 'MiniMax CN Anthropic', url: 'https://api.minimaxi.com/anthropic' },
        { mode: 'payg', protocol: 'responses', label: 'MiniMax CN Responses', url: 'https://api.minimaxi.com/v1' },
        { mode: 'payg', protocol: 'chat_completions', label: 'MiniMax Intl', url: 'https://api.minimax.io/v1' },
        { mode: 'payg', protocol: 'anthropic', label: 'MiniMax Intl Anthropic', url: 'https://api.minimax.io/anthropic' },
        { mode: 'payg', protocol: 'responses', label: 'MiniMax Intl Responses', url: 'https://api.minimax.io/v1' },
        { mode: 'coding', protocol: 'chat_completions', label: 'MiniMax Coding CN', url: 'https://api.minimaxi.com/v1' },
        { mode: 'coding', protocol: 'anthropic', label: 'MiniMax Coding CN Anthropic', url: 'https://api.minimaxi.com/anthropic' },
        { mode: 'coding', protocol: 'responses', label: 'MiniMax Coding CN Responses', url: 'https://api.minimaxi.com/v1' },
        { mode: 'coding', protocol: 'chat_completions', label: 'MiniMax Coding Intl', url: 'https://api.minimax.io/v1' },
        { mode: 'coding', protocol: 'anthropic', label: 'MiniMax Coding Intl Anthropic', url: 'https://api.minimax.io/anthropic' },
        { mode: 'coding', protocol: 'responses', label: 'MiniMax Coding Intl Responses', url: 'https://api.minimax.io/v1' }
    ]
};
/**
 * 返回指定供应商 + 接入模式 + API 协议的默认 base url（来自平台清单）。
 * 该模式不提供原生 Responses 时回落 Chat Completions 基址；不提供 Anthropic 时为空串。
 */
export function defaultCNBaseUrl(platform, mode, protocol = 'chat_completions') {
    const baseUrls = getProviderMode(platform, mode)?.base_urls;
    if (!baseUrls)
        return '';
    const chatBaseUrl = baseUrls.chat_completions ?? '';
    switch (protocol) {
        case 'anthropic':
            return baseUrls.anthropic ?? '';
        case 'responses':
            return baseUrls.responses || chatBaseUrl;
        default:
            return chatBaseUrl;
    }
}
/** 返回自适应模式下各原生协议的默认端点；不提供的协议为空串。 */
export function defaultCNAdaptiveBaseUrls(platform, mode) {
    const baseUrls = getProviderMode(platform, mode)?.base_urls ?? {};
    return {
        chat_completions: baseUrls.chat_completions ?? '',
        anthropic: baseUrls.anthropic ?? '',
        responses: baseUrls.responses ?? ''
    };
}
// Command Code、Cline 的积分与用量接口只查官方主机上的 API Key 账号（与后端
// commandCodeUsageSupported / clineAccountAPISupported 一致）：自定义中转的 Key 不发往
// 官方接口，后端直接拒绝探测，单元格只会显示一行报错。
const OFFICIAL_ACCOUNT_API_PLATFORMS = ['command_code', 'cline'];
function cnUsageCellAccountMode(account) {
    const mode = account.credentials?.account_mode;
    return typeof mode === 'string' ? mode : '';
}
/** 与后端 Account.GetAPIProtocol 一致：按模型分流的供应商未指定原生协议时为 adaptive。 */
function accountUsesAdaptiveProtocol(platform, mode, protocol) {
    if (protocol === 'adaptive')
        return true;
    if (protocol === 'chat_completions' || protocol === 'anthropic')
        return false;
    if (protocol === 'responses' && cnSupportsNativeResponses(platform, mode))
        return false;
    return providerRoutesByModel(platform);
}
/**
 * 账号实际使用的 Chat Completions 端点（与后端 Account.GetOpenAIBaseURL 一致）：adaptive
 * 账号优先分协议端点，其次 base_url，都没填时为平台清单的默认端点。
 */
function accountChatBaseUrl(account, mode) {
    const credentials = account.credentials;
    const baseUrls = credentials?.api_base_urls;
    if (baseUrls && typeof baseUrls === 'object' && accountUsesAdaptiveProtocol(account.platform, mode, credentials?.api_protocol)) {
        const chat = baseUrls.chat_completions;
        if (typeof chat === 'string' && chat.trim())
            return chat.trim();
    }
    const baseUrl = credentials?.base_url;
    if (typeof baseUrl === 'string' && baseUrl.trim())
        return baseUrl.trim();
    return defaultCNBaseUrl(account.platform, mode);
}
/** 官方主机即平台清单默认端点的主机，且必须是 https。 */
function officialAccountAPISupported(account, mode) {
    if (account.type !== 'apikey')
        return false;
    try {
        const target = new URL(accountChatBaseUrl(account, mode));
        const official = new URL(defaultCNBaseUrl(account.platform, mode));
        return target.protocol === 'https:' && target.hostname === official.hostname;
    }
    catch {
        return false;
    }
}
export function cnQuotaCellVisible(account) {
    const { platform } = account;
    const mode = cnUsageCellAccountMode(account);
    if (platform === 'opencode_go')
        return mode !== 'zen';
    // Command Code、Cline：订阅窗口与积分由一次探测同时刷新。
    if (OFFICIAL_ACCOUNT_API_PLATFORMS.includes(platform))
        return officialAccountAPISupported(account, mode);
    return (platform === 'kimi' || platform === 'zhipu' || platform === 'minimax') && mode === 'coding';
}
export function cnBalanceCellVisible(account) {
    const { platform } = account;
    const mode = cnUsageCellAccountMode(account);
    if (OFFICIAL_ACCOUNT_API_PLATFORMS.includes(platform))
        return officialAccountAPISupported(account, mode);
    return (platform === 'kimi' || platform === 'deepseek') && mode !== 'coding';
}
/**
 * 将请求头覆写写入 credentials。
 * create 模式：关闭时不写入任何字段；edit 模式：关闭时删除字段（全量替换语义）。
 */
export function applyHeaderOverride(credentials, enabled, rows, mode) {
    if (enabled) {
        credentials[HEADER_OVERRIDE_ENABLED_CREDENTIAL_KEY] = true;
        credentials[HEADER_OVERRIDES_CREDENTIAL_KEY] = buildHeaderOverridesObject(rows);
    }
    else if (mode === 'edit') {
        delete credentials[HEADER_OVERRIDE_ENABLED_CREDENTIAL_KEY];
        delete credentials[HEADER_OVERRIDES_CREDENTIAL_KEY];
    }
}
/**
 * plan_type 值的友好显示标签（ChatGPT 档位命名）。
 * 与 PlatformTypeBadge 共用 openAIPlanTypeLabel，避免两处映射漂移；
 * canonical 值 chatgptpro 显示为 Pro 200，team 显示为 Business。未知值原样返回。
 */
export function planTypeDisplayLabel(value) {
    return openAIPlanTypeLabel(value) || value;
}
/**
 * 从凭据里读取 plan_type，仅接受字符串（脏数据 42/true 等一律视为空，
 * 避免被当作合法自定义项保留）。
 */
export function readPlanType(credentials) {
    const v = credentials?.plan_type;
    return typeof v === 'string' ? v : '';
}
/** Build SKU-preserving choices; aliases replace only the same canonical SKU. */
export function buildPlanTypeOptions(current, clearLabel) {
    const cur = (current || '').trim();
    const key = openAIPlanTypeKey(cur);
    const opts = [{ value: '', label: clearLabel }];
    for (const preset of openAIPlanTypes) {
        const value = cur && key === openAIPlanTypeKey(preset) ? cur : preset;
        opts.push({ value, label: planTypeDisplayLabel(value) });
    }
    if (cur && !opts.some(option => option.value === cur)) {
        opts.push({ value: cur, label: planTypeDisplayLabel(cur) });
    }
    return opts;
}
/**
 * 把手动选择的 plan_type 写入凭据：非空则设置，空则删除该键（清空/自动识别）。
 * 直接修改传入对象并返回。
 */
export function applyPlanType(credentials, planType) {
    const pt = (planType || '').trim();
    if (pt) {
        credentials.plan_type = pt;
    }
    else {
        delete credentials.plan_type;
    }
    return credentials;
}
