import { describe, expect, it } from 'vitest'
import { proxyQualityOverallClass, proxyQualityOverallLabelKey, summarizeProxyQuality } from '../proxyQuality'

describe('proxy quality presentation', () => {
  it('summarizes with challenge > failed > warn > healthy precedence', () => {
    expect(summarizeProxyQuality({ challenge_count: 1, failed_count: 1, warn_count: 1 })).toBe('challenge')
    expect(summarizeProxyQuality({ challenge_count: 0, failed_count: 2, warn_count: 1 })).toBe('failed')
    expect(summarizeProxyQuality({ challenge_count: 0, failed_count: 0, warn_count: 1 })).toBe('warn')
    expect(summarizeProxyQuality({ challenge_count: 0, failed_count: 0, warn_count: 0 })).toBe('healthy')
  })

  it('maps overall statuses to badge classes and i18n keys', () => {
    expect(proxyQualityOverallClass('healthy')).toBe('badge-success')
    expect(proxyQualityOverallClass('warn')).toBe('badge-warning')
    expect(proxyQualityOverallClass('challenge')).toBe('badge-danger')
    expect(proxyQualityOverallClass(undefined)).toBe('badge-danger')
    expect(proxyQualityOverallLabelKey('healthy')).toBe('admin.proxies.qualityStatusHealthy')
    expect(proxyQualityOverallLabelKey('warn')).toBe('admin.proxies.qualityStatusWarn')
    expect(proxyQualityOverallLabelKey('challenge')).toBe('admin.proxies.qualityStatusChallenge')
    expect(proxyQualityOverallLabelKey('failed')).toBe('admin.proxies.qualityStatusFail')
  })
})
