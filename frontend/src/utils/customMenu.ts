import type { CustomMenuItem } from '@/types'

export function getCustomMenuLabel(item: CustomMenuItem, locale: string): string {
  const preferredLabel = locale.toLowerCase().startsWith('zh') ? item.label_zh : item.label_en
  return preferredLabel?.trim() || item.label?.trim() || item.label_en?.trim() || item.label_zh?.trim() || ''
}
