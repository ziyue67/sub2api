import { apiClient } from './client';
export const BANANA_PROMPTS_URL = 'https://raw.githubusercontent.com/glidea/banana-prompt-quicker/main/prompts.json';
export const AWESOME_GPT_IMAGE_2_PROMPTS_ZH_README_URL = 'https://raw.githubusercontent.com/EvoLinkAI/awesome-gpt-image-2-prompts/main/README_zh-CN.md';
export const AWESOME_GPT_IMAGE_2_PROMPTS_EN_README_URL = 'https://raw.githubusercontent.com/EvoLinkAI/awesome-gpt-image-2-prompts/main/README.md';
const AWESOME_GPT_IMAGE_2_PROMPTS_RAW_BASE_URL = 'https://raw.githubusercontent.com/EvoLinkAI/awesome-gpt-image-2-prompts/main/';
export const PROMPT_MARKET_SOURCE_OPTIONS = [
    { value: 'all', label: 'All' },
    { value: 'favorites', label: 'Favorites' },
    { value: 'banana-prompt-quicker', label: 'banana-prompt-quicker' },
    { value: 'awesome-gpt-image-2-prompts', label: 'awesome-gpt-image-2-prompts' },
];
const MARKDOWN_CASE_HEADING_PATTERN = /^### Case\s+(\d+):\s+\[([^\]]+)]\(([^)]+)\)\s+\(by\s+\[([^\]]+)]\(([^)]+)\)\)/;
const MARKDOWN_IMAGE_PATTERN = /<img\s+[^>]*src=["']([^"']+)["'][^>]*>/i;
const MARKDOWN_PROMPT_PATTERN = /\*{2,}\s*(?:Prompt|提示词)\s*[:：]\s*\*{2,}\s*\n\s*```(?:\w+)?\n([\s\S]*?)\n```/i;
const IGNORED_MARKET_README_HEADINGS = new Set(['简介', '最新动态', 'Menu', '致谢', 'Star History']);
const NSFW_TEXT_PATTERN = /\b(nsfw|nude|naked|lingerie|erotic|seductive|sexy|cleavage|underwear|panties|bra|bikini|ahegao|explicit|sensual|fetish|nipples?|genitals?|buttocks?|thong|topless)\b|裸|色情|情色|性感|诱惑|内衣|内裤|乳|胸|臀|私处|泳衣|比基尼|情趣|丁字裤|翻白眼|吐舌|妩媚|暧昧/i;
function normalizePromptMode(value) {
    return value === 'edit' ? 'edit' : 'generate';
}
function buildPromptId(item, index) {
    return [item.title, item.author, index]
        .map((part) => String(part || '').trim())
        .filter(Boolean)
        .join(':');
}
function normalizeReferenceImageUrls(value) {
    if (!Array.isArray(value))
        return [];
    return value.filter((url) => typeof url === 'string' && url.trim().length > 0);
}
function isNsfwPrompt(category, title, prompt) {
    return category === 'NSFW' || NSFW_TEXT_PATTERN.test(`${category}\n${title}\n${prompt}`);
}
function normalizePrompt(item, index) {
    if (typeof item.title !== 'string' ||
        typeof item.preview !== 'string' ||
        typeof item.prompt !== 'string' ||
        typeof item.author !== 'string') {
        return null;
    }
    const title = item.title.trim();
    const preview = item.preview.trim();
    const prompt = item.prompt.trim();
    const author = item.author.trim();
    const category = typeof item.category === 'string' && item.category.trim() ? item.category.trim() : '未分类';
    if (!title || !preview || !prompt || !author)
        return null;
    return {
        id: `banana-prompt-quicker:${buildPromptId(item, index)}`,
        title,
        preview,
        prompt,
        author,
        referenceImageUrls: normalizeReferenceImageUrls(item.reference_image_urls),
        link: typeof item.link === 'string' && item.link.trim() ? item.link.trim() : undefined,
        mode: normalizePromptMode(item.mode),
        category,
        subCategory: typeof item.sub_category === 'string' && item.sub_category.trim() ? item.sub_category.trim() : undefined,
        created: typeof item.created === 'string' && item.created.trim() ? item.created.trim() : undefined,
        source: 'banana-prompt-quicker',
        sourceLabel: 'banana-prompt-quicker',
        isNsfw: category === 'NSFW',
    };
}
function normalizeMarkdownImageUrl(value) {
    const imageUrl = value.trim();
    if (!imageUrl)
        return '';
    if (/^https?:\/\//i.test(imageUrl))
        return imageUrl;
    return new URL(imageUrl.replace(/^\.\//, ''), AWESOME_GPT_IMAGE_2_PROMPTS_RAW_BASE_URL).toString();
}
function buildAwesomePromptMergeKey(link, preview) {
    return `${link.trim()}|${preview.trim()}`;
}
function cleanMarkdownHeading(value) {
    return value
        .replace(/^#+\s*/, '')
        .replace(/^[\p{Emoji_Presentation}\p{Extended_Pictographic}]\s*/u, '')
        .trim();
}
function normalizeAwesomePromptSection(section, category, language) {
    const heading = section.match(MARKDOWN_CASE_HEADING_PATTERN);
    const image = section.match(MARKDOWN_IMAGE_PATTERN);
    const promptBlock = section.match(MARKDOWN_PROMPT_PATTERN);
    if (!heading || !image || !promptBlock)
        return null;
    const caseNumber = heading[1].trim();
    const title = heading[2].trim();
    const link = heading[3].trim();
    const author = heading[4].trim();
    const preview = normalizeMarkdownImageUrl(image[1]);
    const prompt = promptBlock[1].trim();
    if (!caseNumber || !title || !preview || !prompt || !author)
        return null;
    return {
        id: `awesome-gpt-image-2-prompts:${buildAwesomePromptMergeKey(link, preview)}`,
        title,
        preview,
        referenceImageUrls: [],
        prompt,
        author,
        link,
        mode: 'generate',
        category,
        subCategory: `Case ${caseNumber}`,
        source: 'awesome-gpt-image-2-prompts',
        sourceLabel: 'awesome-gpt-image-2-prompts',
        isNsfw: isNsfwPrompt(category, title, prompt),
        language,
        mergeKey: buildAwesomePromptMergeKey(link, preview),
        localizations: {
            [language]: {
                title,
                prompt,
                category,
                subCategory: `Case ${caseNumber}`,
            },
        },
    };
}
function parseAwesomePrompts(markdown, language) {
    const lines = markdown.split(/\r?\n/);
    const prompts = [];
    let activeCategory = '未分类';
    for (let index = 0; index < lines.length; index += 1) {
        const line = lines[index];
        if (line.startsWith('## ')) {
            const heading = cleanMarkdownHeading(line);
            if (heading && !IGNORED_MARKET_README_HEADINGS.has(heading))
                activeCategory = heading;
            continue;
        }
        if (!line.startsWith('### Case '))
            continue;
        const sectionStart = index;
        let sectionEnd = lines.length;
        for (let nextIndex = index + 1; nextIndex < lines.length; nextIndex += 1) {
            if (lines[nextIndex].startsWith('### Case ') || lines[nextIndex].startsWith('## ')) {
                sectionEnd = nextIndex;
                break;
            }
        }
        const prompt = normalizeAwesomePromptSection(lines.slice(sectionStart, sectionEnd).join('\n'), activeCategory, language);
        if (prompt)
            prompts.push(prompt);
        index = sectionEnd - 1;
    }
    return prompts;
}
function mergeAwesomePrompts(...groups) {
    const promptsByKey = new Map();
    groups.flat().forEach((prompt) => {
        const current = promptsByKey.get(prompt.mergeKey);
        if (!current) {
            promptsByKey.set(prompt.mergeKey, prompt);
            return;
        }
        current.localizations = {
            ...current.localizations,
            ...prompt.localizations,
        };
        current.isNsfw = current.isNsfw || prompt.isNsfw;
        if (current.language !== 'zh-CN' && prompt.language === 'zh-CN') {
            current.title = prompt.title;
            current.prompt = prompt.prompt;
            current.category = prompt.category;
            current.subCategory = prompt.subCategory;
            current.language = prompt.language;
        }
    });
    return [...promptsByKey.values()].map(({ language: _language, mergeKey: _mergeKey, ...prompt }) => prompt);
}
export function promptFavoriteKey(prompt) {
    return `${prompt.source}:${prompt.id}`;
}
export function promptFavoriteRecordKey(favorite) {
    return `${favorite.source}:${favorite.prompt_id}`;
}
export function promptFavoriteToBananaPrompt(favorite) {
    return {
        id: favorite.prompt_id,
        title: favorite.title,
        preview: favorite.preview,
        referenceImageUrls: favorite.reference_image_urls || [],
        prompt: favorite.prompt,
        author: favorite.author,
        link: favorite.link,
        mode: favorite.mode,
        category: favorite.category,
        subCategory: favorite.sub_category,
        created: favorite.created,
        source: favorite.source,
        sourceLabel: favorite.source_label,
        isNsfw: favorite.is_nsfw,
        localizations: normalizeFavoriteLocalizations(favorite.localizations),
    };
}
export function bananaPromptToFavoritePayload(prompt) {
    return {
        prompt_id: prompt.id,
        source: prompt.source,
        title: prompt.title,
        preview: prompt.preview,
        reference_image_urls: prompt.referenceImageUrls,
        prompt: prompt.prompt,
        author: prompt.author,
        link: prompt.link,
        mode: prompt.mode,
        category: prompt.category,
        sub_category: prompt.subCategory,
        created: prompt.created,
        source_label: prompt.sourceLabel,
        is_nsfw: prompt.isNsfw,
        localizations: prompt.localizations ? localizationsToPayload(prompt.localizations) : undefined,
    };
}
function normalizeFavoriteLocalizations(value) {
    if (!value)
        return undefined;
    const localizations = {};
    for (const language of ['zh-CN', 'en']) {
        const item = value[language];
        if (!item)
            continue;
        localizations[language] = {
            title: item.title,
            prompt: item.prompt,
            category: item.category,
            subCategory: item.subCategory ?? item.sub_category,
            created: item.created,
        };
    }
    return Object.keys(localizations).length > 0 ? localizations : undefined;
}
function localizationsToPayload(localizations) {
    const payload = {};
    for (const [language, item] of Object.entries(localizations)) {
        if (!item)
            continue;
        payload[language] = {
            title: item.title,
            prompt: item.prompt,
            category: item.category,
            sub_category: item.subCategory,
            created: item.created,
        };
    }
    return payload;
}
export async function fetchBananaPrompts(signal) {
    const response = await fetch(BANANA_PROMPTS_URL, {
        signal,
        headers: { Accept: 'application/json' },
    });
    if (!response.ok)
        throw new Error(`Failed to load banana-prompt-quicker: ${response.status}`);
    const data = await response.json();
    if (!Array.isArray(data))
        throw new Error('Invalid prompt market data');
    return data.flatMap((item, index) => {
        const prompt = normalizePrompt(item, index);
        return prompt ? [prompt] : [];
    });
}
export async function fetchAwesomeGptImage2Prompts(signal) {
    const [zhResponse, enResponse] = await Promise.all([
        fetch(AWESOME_GPT_IMAGE_2_PROMPTS_ZH_README_URL, {
            signal,
            headers: { Accept: 'text/markdown,text/plain' },
        }),
        fetch(AWESOME_GPT_IMAGE_2_PROMPTS_EN_README_URL, {
            signal,
            headers: { Accept: 'text/markdown,text/plain' },
        }),
    ]);
    if (!zhResponse.ok)
        throw new Error(`Failed to load awesome-gpt-image-2-prompts zh-CN: ${zhResponse.status}`);
    if (!enResponse.ok)
        throw new Error(`Failed to load awesome-gpt-image-2-prompts en: ${enResponse.status}`);
    const [zhMarkdown, enMarkdown] = await Promise.all([zhResponse.text(), enResponse.text()]);
    return mergeAwesomePrompts(parseAwesomePrompts(zhMarkdown, 'zh-CN'), parseAwesomePrompts(enMarkdown, 'en'));
}
export async function fetchPromptMarketPrompts(signal) {
    const [bananaPrompts, awesomePrompts] = await Promise.all([
        fetchBananaPrompts(signal),
        fetchAwesomeGptImage2Prompts(signal),
    ]);
    return [...bananaPrompts, ...awesomePrompts];
}
export async function fetchPromptFavorites(signal) {
    const { data } = await apiClient.get('/user/prompt-favorites', {
        signal,
    });
    return data;
}
export async function createPromptFavorite(prompt) {
    const { data } = await apiClient.post('/user/prompt-favorites', bananaPromptToFavoritePayload(prompt));
    return data;
}
export async function deletePromptFavorite(favoriteId) {
    const { data } = await apiClient.delete(`/user/prompt-favorites/${encodeURIComponent(String(favoriteId))}`);
    return data;
}
