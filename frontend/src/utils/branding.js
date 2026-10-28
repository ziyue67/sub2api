import { sanitizeUrl } from '@/utils/url';
export function updateFavicon(logoUrl) {
    const sanitizedLogoUrl = sanitizeUrl(logoUrl, {
        allowRelative: true,
        allowDataUrl: true,
    });
    if (!sanitizedLogoUrl) {
        return;
    }
    let link = document.querySelector('link[rel="icon"]');
    if (!link) {
        link = document.createElement('link');
        link.rel = 'icon';
        document.head.appendChild(link);
    }
    link.type = sanitizedLogoUrl.endsWith('.svg') ? 'image/svg+xml' : 'image/x-icon';
    link.href = sanitizedLogoUrl;
}
