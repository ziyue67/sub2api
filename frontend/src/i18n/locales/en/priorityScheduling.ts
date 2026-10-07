export default {
  oauthQuotaPriority: 'Consume OAuth quota first; move trailing API keys to standby',
  oauthQuotaHint: 'When healthy OAuth accounts below the 7d usage threshold have spare capacity, move API keys from the end of the routing order to standby. The count follows OAuth capacity. Full, exhausted or unavailable OAuth accounts automatically release standby keys. Manual account status is unchanged. Requires valid quality evidence and quota data less than 5 minutes old.',
  oauthQuotaThreshold: 'OAuth preference cutoff (7d used %)',
  oauthQuotaRoles: { preferred: 'At selection: OAuth quota preference candidate', standby: 'At selection: API key on automatic standby, available for overflow' },

  evaluatedAt: 'Scores evaluated', historyObservedAt: 'History read',
  historyRetry: 'History refresh failed; recent cached evidence remains in use until expiry. Retry shortly.',
  historyStates: { ready: 'History ready', stale: 'Using recent cached history while awaiting an update.', partial: 'Some candidates are still loading; known history remains in use.', loading: 'Loading history; missing evidence remains unknown.', error: 'History could not be read. Refresh to retry shortly.', unavailable: 'History is unavailable on this instance.', limited: 'Candidate or cache limit reached; using live capacity.' },


  profit: 'Recent profit / margin',
  economics: { usage: 'Usage records', rate: 'Rate estimate', unknown: 'Insufficient profit samples' },
  priority: 'Priority',
  balanceProtocols: 'Balance compatible BPS and native accounts',
  balanceProtocolsHint: 'Only group-, model-, quota- and transport-compatible accounts participate. Account protocols stay intact and BPS is never enabled automatically. Disable to restore BPS preference.',
  modelAliases: 'Treat model mappings as aliases while keeping the default model scope',
  modelAliasesHint: 'OpenAI OAuth only. Unmatched aliases use default model admission; when disabled, nonempty mappings remain allowlists. Enable only for accounts intended to serve those models.',
  changeModelScope: 'Change the selected OAuth accounts’ model mapping scope',
  bulkModelAliasesHint: 'Changes scope only, preserving each account’s aliases and credentials. Does not enable BPS. Use to repair new accounts restricted to one automatic alias.',
  selectionWeight: 'Selection weight', boundGroups: 'Bound groups', explorationEligible: 'Eligible for limited exploration',

  title: 'Priority scheduling', description: 'Meet experience targets, then balance quality, latency, capacity and cost.', enabled: 'Enable priority scheduling',
  scopeNote: 'Applies to freely routed OpenAI text requests. Session/task ownership, protocol compatibility, model permissions, rate limits and profit gates still apply. Images, video and other platforms keep their existing scheduling.',
  modes: { experience: 'Experience first', balanced: 'Balanced', profit: 'Profit first', custom: 'Custom' },
  strategy: 'Scheduling strategy', weights: 'Score weights', quality_weight: 'Quality', latency_weight: 'Latency', load_weight: 'Available capacity', cost_weight: 'Profit / cost',
  thresholds: 'Experience targets and observation window', target_ttft_ms: 'P90 first-token target (ms)', max_load_percent: 'Concurrency threshold (%)', min_quality_percent: 'Quality pass target (%)', window_minutes: 'Usage window (minutes)', min_samples: 'Minimum latency / profit samples', quality_max_age_hours: 'Quality freshness (hours)',
  groupIds: 'Group IDs', groupHint: 'Comma-separated; empty means all groups.', models: 'Models', modelHint: 'One exact requested model name per line; empty means all models.',
  rule: 'Avoid known quality, error and loss risks, then prefer lower projected concurrency and RPM utilization. A small profit advantage cannot keep sending work to busy accounts when lower-load peers exist. Similar-capacity peers share traffic by experience tier, priority and weights; OAuth accounts bound to fewer groups get more preference. Safe new accounts receive limited exploration. Movable sessions may temporarily spill over while retaining their binding.',
  costHint: 'Profit = user charges − estimated cost. Estimated cost = account statistics price or base cost summed for the same group and model × the current account cost multiplier. Set it in account editing (default 0.1). When following is enabled, successful upstream probes update the same value. Turn it off to retain a manual cost; failures or expiry keep the saved value. Account and user billing remain independent. Changes re-estimate recent profit without rewriting usage logs. Margin = profit / user charges.',
  save: 'Save configuration', saving: 'Saving…', saved: 'Configuration saved', loading: 'Loading…', retry: 'Retry', error: 'Could not load or save. Please retry.', invalid: 'Check group IDs and parameter ranges',
  recent: 'Latest candidate scores', refresh: 'Refresh scores', empty: 'No scores yet. Eligible requests requiring free routing generate scores after enabling.', historyPending: 'History is not ready; live capacity balancing remains active. Missing history stays unknown and refreshes in the background.',
  snapshotHint: 'Shows this instance’s latest pool, up to 100 accounts. Refresh asynchronously reads history and re-evaluates scores; candidates, configuration and load remain those observed at selection time. Re-evaluated scores do not describe the original routing decision. Rows are ordered by risk, projected utilization band and preference. Similar-capacity peers use weighted selection; row order is not a fixed allocation. Live candidates remain visible while history is loading.',
  model: 'Model', group: 'Group', account: 'Account', score: 'Score', tier: 'Status', latency: 'P90 first token', load: 'Concurrency', rate: 'Cost rate', quality: 'Quality passed', samples: 'samples', unknown: 'Unknown',
  tiers: { eligible: 'Targets met', insufficient: 'Insufficient data', degraded: 'Targets missed' },
  reasons: { quality_below_target: 'Quality below target', quality_unknown: 'No fresh quality result', latency_above_target: 'Latency above target', latency_insufficient: 'Insufficient latency samples', busy: 'High concurrency or queueing', load_unknown: 'Concurrency unknown', cost_unknown: 'Cost unknown', recent_errors: 'Elevated recent errors', historical_loss: 'Recent charges below theoretical cost', profit_insufficient: 'Insufficient profit samples' }
}
