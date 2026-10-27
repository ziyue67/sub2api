import { describe, expect, it } from 'vitest'
import { getCustomMenuLabel } from '@/utils/customMenu'

const menuItem = {
  id: 'help',
  label: 'Legacy name',
  label_zh: '帮助中心',
  label_en: 'Help Center',
  icon_svg: '',
  url: 'https://example.com',
  visibility: 'user' as const,
  sort_order: 0,
}

describe('getCustomMenuLabel', () => {
  it('selects the localized label for the active language', () => {
    expect(getCustomMenuLabel(menuItem, 'zh')).toBe('帮助中心')
    expect(getCustomMenuLabel(menuItem, 'en')).toBe('Help Center')
  })

  it('falls back to the legacy label, then to another available locale', () => {
    expect(getCustomMenuLabel({ ...menuItem, label_zh: '', label_en: '' }, 'zh')).toBe('Legacy name')
    expect(getCustomMenuLabel({ ...menuItem, label: '', label_zh: '帮助中心', label_en: '' }, 'en'))
      .toBe('帮助中心')
  })
})
