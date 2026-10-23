export default {
  safetyPromptAudit: {
    nav: 'Safety injection audit',
    title: 'Safety injection audit',
    description: 'Inject a fixed safety policy into OpenAI OAuth requests and, when the model returns this request\'s exact marker, replace it with a fixed refusal and record a reviewable event.',
    module: 'Module settings',
    enabled: 'Enable safety injection',
    enabledHint: 'Inject the fixed safety policy into supported OpenAI OAuth outbound requests on this instance. Off by default.',
    signalEnabled: 'Enable independent refusal detection',
    signalHint: 'When the complete assistant reply equals this request\'s sp- marker, replace it with the fixed refusal and record an event. Responses transport only.',
    retentionDays: 'Event retention (days)',
    retentionHint: '1–90 days, up to the latest 500 events. Expired or evicted events cannot be recovered.',
    save: 'Save settings',
    saving: 'Saving…',
    saved: 'Settings saved',
    refresh: 'Refresh',
    loading: 'Loading…',
    loadFailed: 'Could not read the safety injection audit. Please retry.',
    saveFailed: 'Could not save safety injection settings. Check the values and retry.',
    storageReady: 'Event storage OK · {count}/{limit} events',
    storageWarning: 'Storage warning: {warning}',
    disabledNotice: 'Safety injection is disabled; retained events are still visible.',
    filters: {
      from: 'From date (local time)',
      to: 'To date (inclusive)',
      search: 'Search event ID / request ID / model / summary',
      all: 'All retained events',
      today: 'Today'
    },
    count: 'Showing {shown} / {total}. Exact model hits, not confirmed violations.',
    badRange: 'The start date cannot be later than the end date.',
    empty: 'No events match these filters.',
    table: {
      time: 'Time / event ID',
      model: 'Model / upstream account',
      user: 'Request user',
      summary: 'Redacted summary'
    },
    unknownUser: 'Unknown (not linked by host)',
    requestId: 'Request ID',
    upstreamAccount: 'Upstream account',
    exportJson: 'Export JSON',
    exportCsv: 'Export CSV',
    noSummary: 'No text summary',
    scopeNote: 'Covers only OpenAI OAuth requests passing through supported send points; it does not change upstream cyber_policy decisions or auto-ban accounts.'
  }
}
