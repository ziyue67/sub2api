import type { ModelSquareEntry } from '@/api/modelSquare'
import type { UserSupportedModelPricing } from '@/api/channels'

export type ModelCapability =
  | 'vision'
  | 'tool_call'
  | 'reasoning'
  | 'audio'
  | 'image_gen'
  | 'embedding'

export type PriceRange = 'all' | 'free' | 'lt1' | '1to5' | '5to15' | 'gt15'

export type ContextRange = 'all' | 'lt32k' | '32kTo128k' | '128kTo256k' | 'gt256k'

export type BillingTypeFilter = 'all' | 'tokens' | 'request' | 'intervals'

export type SortOption =
  | 'default'
  | 'price_asc'
  | 'price_desc'
  | 'output_price_asc'
  | 'output_price_desc'
  | 'channels_desc'
  | 'name_asc'

export type ViewMode = 'grid' | 'table'

export interface ModelSquareModel {
  key: string
  name: string
  platform: string
  entries: ModelSquareEntry[]
  channels: ModelSquareChannel[]

  // Augmented fields for OpenModel-style directory & pricing
  contextWindow?: string
  contextTokens?: number
  capabilities?: ModelCapability[]
  minInputPrice?: number | null
  maxInputPrice?: number | null
  minOutputPrice?: number | null
  maxOutputPrice?: number | null
  minCacheWritePrice?: number | null
  minCacheReadPrice?: number | null
  isRequestBilling?: boolean
  hasIntervals?: boolean
  bestMultiplier?: number | null
  totalAccounts?: number
}

export interface ModelSquareChannel {
  key: string
  name: string
  entries: ModelSquareEntry[]
  pricing: UserSupportedModelPricing | null
}
