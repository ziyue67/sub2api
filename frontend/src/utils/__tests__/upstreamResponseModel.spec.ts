import { describe, expect, it } from 'vitest'
import { showUpstreamResponseModelToUsers, userVisibleUpstreamResponseModel } from '../upstreamResponseModel'

describe('upstream response model visibility', () => {
  it('stays hidden unless the admin setting is explicitly on', () => {
    expect(showUpstreamResponseModelToUsers(undefined)).toBe(false)
    expect(showUpstreamResponseModelToUsers({})).toBe(false)
    expect(showUpstreamResponseModelToUsers({ usage_show_upstream_response_model_to_users: false })).toBe(false)
    expect(showUpstreamResponseModelToUsers({ usage_show_upstream_response_model_to_users: true })).toBe(true)
  })

  it('shows a different response model only when users are allowed to see it', () => {
    const row = { model: 'gpt-5.4', upstream_response_model: ' gpt-5.4-actual ' }
    expect(userVisibleUpstreamResponseModel(undefined, row)).toBe('')
    expect(userVisibleUpstreamResponseModel({ usage_show_upstream_response_model_to_users: true }, row)).toBe('gpt-5.4-actual')
    expect(userVisibleUpstreamResponseModel(
      { usage_show_upstream_response_model_to_users: true },
      { model: 'gpt-5.4', upstream_response_model: 'gpt-5.4' },
    )).toBe('')
  })
})
