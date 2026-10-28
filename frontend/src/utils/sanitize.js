import DOMPurify from 'dompurify';
export function sanitizeSvg(svg) {
    if (!svg)
        return '';
    return DOMPurify.sanitize(svg, { USE_PROFILES: { svg: true, svgFilters: true } });
}
