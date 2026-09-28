interface AccountRPMSettings {
  enabled: boolean
  baseRpm: number | null
  strict?: boolean
  strategy?: 'tiered' | 'sticky_exempt'
  stickyBuffer?: number | null
}

// Single edits replace extra; bulk edits merge it. Merge payloads must send
// explicit empty values to clear settings that already exist in the database.
export function applyAccountRPMSettings(
  extra: Record<string, unknown>,
  settings: AccountRPMSettings,
  mode: 'replace' | 'merge' = 'replace'
): void {
  const clear = (key: string, empty: unknown) => {
    if (mode === 'merge') extra[key] = empty
    else delete extra[key]
  }
  if (!settings.enabled) {
    clear('base_rpm', 0)
    clear('rpm_strategy', '')
    clear('rpm_sticky_buffer', 0)
    return
  }
  extra.base_rpm = settings.baseRpm != null && settings.baseRpm > 0 ? settings.baseRpm : 15
  if (settings.strict) {
    clear('rpm_strategy', '')
    clear('rpm_sticky_buffer', 0)
    return
  }
  extra.rpm_strategy = settings.strategy ?? 'tiered'
  if (settings.stickyBuffer != null && settings.stickyBuffer > 0) {
    extra.rpm_sticky_buffer = settings.stickyBuffer
  } else if (mode === 'replace') {
    delete extra.rpm_sticky_buffer
  }
}
