export const DEEPSEEK_WS_MODE_OFF = 'off' as const
export const DEEPSEEK_WS_MODE_HTTP_BRIDGE = 'http_bridge' as const

export type DeepSeekWSMode =
  | typeof DEEPSEEK_WS_MODE_OFF
  | typeof DEEPSEEK_WS_MODE_HTTP_BRIDGE

export const normalizeDeepSeekWSMode = (value: unknown): DeepSeekWSMode | null => {
  if (value === DEEPSEEK_WS_MODE_OFF || value === DEEPSEEK_WS_MODE_HTTP_BRIDGE) {
    return value
  }
  return null
}

export const resolveDeepSeekWSModeFromExtra = (
  extra: Record<string, unknown> | null | undefined,
  globalBridgeEnabled: boolean
): DeepSeekWSMode => {
  if (extra && Object.prototype.hasOwnProperty.call(extra, 'deepseek_responses_websockets_v2_mode')) {
    return normalizeDeepSeekWSMode(extra.deepseek_responses_websockets_v2_mode) ?? DEEPSEEK_WS_MODE_OFF
  }
  return globalBridgeEnabled ? DEEPSEEK_WS_MODE_HTTP_BRIDGE : DEEPSEEK_WS_MODE_OFF
}
