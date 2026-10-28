import { reactive } from 'vue';
const IDLE_ENTRY = { status: 'idle' };
const CACHE_STORAGE_KEY = 'sub2api:ip-geo-cache:v1';
const CACHE_TTL_MS = 24 * 60 * 60 * 1000;
const BATCH_CHUNK_SIZE = 50;
const GEO_SINGLE_URL = 'https://get.geojs.io/v1/ip/geo';
const GEO_BATCH_URL = 'https://get.geojs.io/v1/ip/geo.json';
const cache = reactive(new Map());
function isFreshSuccess(entry) {
    return entry?.status === 'success' && typeof entry.fetchedAt === 'number' && Date.now() - entry.fetchedAt <= CACHE_TTL_MS;
}
function getFreshEntry(ip) {
    const entry = cache.get(ip);
    if (entry?.status === 'success' && !isFreshSuccess(entry)) {
        cache.delete(ip);
        persistToStorage();
        return undefined;
    }
    return entry;
}
export function isPrivateIp(ip) {
    const v4 = ip.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/);
    if (v4) {
        const a = Number(v4[1]);
        const b = Number(v4[2]);
        if (a === 10)
            return true;
        if (a === 127)
            return true;
        if (a === 169 && b === 254)
            return true;
        if (a === 172 && b >= 16 && b <= 31)
            return true;
        if (a === 192 && b === 168)
            return true;
        return false;
    }
    const lower = ip.toLowerCase();
    if (lower === '::1')
        return true;
    const firstSegment = lower.split(':', 1)[0];
    if (/^fe[89ab][0-9a-f]$/.test(firstSegment))
        return true;
    if (/^f[cd][0-9a-f]{2}$/.test(firstSegment))
        return true;
    return false;
}
function loadFromStorage() {
    try {
        const raw = localStorage.getItem(CACHE_STORAGE_KEY);
        if (!raw)
            return;
        const parsed = JSON.parse(raw);
        const now = Date.now();
        for (const [ip, stored] of Object.entries(parsed)) {
            if (!stored || typeof stored.fetchedAt !== 'number')
                continue;
            if (now - stored.fetchedAt > CACHE_TTL_MS)
                continue;
            cache.set(ip, { status: 'success', label: stored.label, detail: stored.detail, fetchedAt: stored.fetchedAt });
        }
    }
    catch {
        // 忽略损坏的本地缓存
    }
}
function persistToStorage() {
    try {
        const toStore = {};
        for (const [ip, entry] of cache.entries()) {
            if (entry.status === 'success' && entry.label && entry.fetchedAt) {
                toStore[ip] = { label: entry.label, detail: entry.detail, fetchedAt: entry.fetchedAt };
            }
        }
        localStorage.setItem(CACHE_STORAGE_KEY, JSON.stringify(toStore));
    }
    catch {
        // 存储写入失败（如隐私模式禁用 localStorage）不影响功能
    }
}
loadFromStorage();
export function getEntry(ip) {
    return getFreshEntry(ip) ?? IDLE_ENTRY;
}
export function formatGeoLabel(detail) {
    const parts = [detail.countryCode, detail.region, detail.city].filter((part) => Boolean(part && part.trim()));
    return parts.join(' · ');
}
function toDetail(raw) {
    return {
        countryCode: raw.country_code,
        region: raw.region,
        city: raw.city,
        organization: raw.organization,
        timezone: raw.timezone,
        accuracy: raw.accuracy,
        latitude: raw.latitude,
        longitude: raw.longitude,
    };
}
function applyResult(ip, raw) {
    if (!raw || !raw.country_code) {
        cache.set(ip, { status: 'error' });
        return;
    }
    const detail = toDetail(raw);
    cache.set(ip, {
        status: 'success',
        label: formatGeoLabel(detail),
        detail,
        fetchedAt: Date.now(),
    });
}
export async function fetchOne(ip, force = false) {
    if (isPrivateIp(ip)) {
        cache.set(ip, { status: 'private' });
        return;
    }
    const existing = getFreshEntry(ip);
    if (!force && (isFreshSuccess(existing) || existing?.status === 'loading')) {
        return;
    }
    cache.set(ip, { status: 'loading' });
    try {
        const response = await fetch(`${GEO_SINGLE_URL}/${encodeURIComponent(ip)}.json`);
        if (!response.ok) {
            cache.set(ip, { status: 'error' });
            return;
        }
        const raw = (await response.json());
        applyResult(ip, raw);
        persistToStorage();
    }
    catch {
        cache.set(ip, { status: 'error' });
    }
}
export async function fetchBatch(ips) {
    const unique = Array.from(new Set(ips));
    const targets = [];
    for (const ip of unique) {
        if (isPrivateIp(ip)) {
            cache.set(ip, { status: 'private' });
            continue;
        }
        const existing = cache.get(ip);
        if (isFreshSuccess(existing) || existing?.status === 'loading')
            continue;
        targets.push(ip);
    }
    if (targets.length === 0)
        return true;
    targets.forEach((ip) => cache.set(ip, { status: 'loading' }));
    let allChunksOk = true;
    for (let i = 0; i < targets.length; i += BATCH_CHUNK_SIZE) {
        const chunk = targets.slice(i, i + BATCH_CHUNK_SIZE);
        try {
            const response = await fetch(`${GEO_BATCH_URL}?ip=${chunk.map(encodeURIComponent).join(',')}`);
            if (!response.ok) {
                chunk.forEach((ip) => cache.set(ip, { status: 'error' }));
                allChunksOk = false;
                continue;
            }
            const results = (await response.json());
            const byIp = new Map(results.map((r) => [r.ip, r]));
            chunk.forEach((ip) => applyResult(ip, byIp.get(ip)));
            persistToStorage();
        }
        catch {
            chunk.forEach((ip) => cache.set(ip, { status: 'error' }));
            allChunksOk = false;
        }
    }
    return allChunksOk;
}
