import type { ModelSquareEntry } from '@/api/modelSquare'
import type { UserSupportedModelPricing } from '@/api/channels'

export interface ModelSquareModel {
  key: string
  name: string
  platform: string
  entries: ModelSquareEntry[]
  channels: ModelSquareChannel[]
}

export interface ModelSquareChannel {
  key: string
  name: string
  entries: ModelSquareEntry[]
  pricing: UserSupportedModelPricing | null
}
