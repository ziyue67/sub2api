export const KEY_GROUP_PROVIDERS = ['anthropic', 'openai', 'domestic', 'other'];
// Classify by the configured upstream platform, never by a group's display name.
const PROVIDER_BY_PLATFORM = {
    anthropic: 'anthropic',
    openai: 'openai',
    kimi: 'domestic',
    zhipu: 'domestic',
    deepseek: 'domestic',
    minimax: 'domestic',
    gemini: 'other',
    grok: 'other',
    antigravity: 'other',
    composite: 'other',
    opencode_go: 'other',
    typesafe: 'other',
    command_code: 'other',
    cline: 'other'
};
export function getKeyGroupProvider(platform) {
    return PROVIDER_BY_PLATFORM[platform] ?? 'other';
}
// Collections use representative provider marks rather than an invented brand logo.
export const KEY_GROUP_PROVIDER_ICONS = {
    anthropic: ['anthropic'],
    openai: ['openai'],
    domestic: ['deepseek', 'kimi'],
    other: ['gemini', 'grok']
};
