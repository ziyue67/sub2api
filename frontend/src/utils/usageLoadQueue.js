/**
 * Usage request scheduler.
 *
 * All platforms execute immediately without queuing — the backend uses
 * passive sampling so upstream 429 rate-limit errors are no longer a concern.
 */
/**
 * Schedule a usage fetch. All requests execute immediately.
 */
export function enqueueUsageRequest(_account, fn) {
    return fn();
}
