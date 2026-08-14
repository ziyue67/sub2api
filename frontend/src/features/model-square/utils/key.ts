import type { ModelSquareEntry } from '@/api/modelSquare'

export function entryKey(entry: ModelSquareEntry) {
  return entry.channel_id + ':' + entry.group.id + ':' + entry.name
}
