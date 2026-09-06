import type { UserSupportedModelPricing } from '@/api/channels'
import type { ModelCapability } from '../types'
import officialModelsRaw from '../data/officialModels.json'

export interface OfficialModelSpec {
  context_tokens?: number
  capabilities?: ModelCapability[]
  input_price?: number | null
  output_price?: number | null
  cache_write_price?: number | null
  cache_read_price?: number | null
  per_request_price?: number | null
  billing_mode?: 'token' | 'image' | 'per_request'
}

const officialModels: Record<string, OfficialModelSpec> = officialModelsRaw as Record<string, OfficialModelSpec>

export function formatContextTokens(tokens: number): string {
  if (tokens >= 1_000_000) {
    const m = tokens / 1_000_000
    return m % 1 === 0 ? `${m}M` : `${m.toFixed(1).replace(/\.0$/, '')}M`
  }
  if (tokens >= 1_000) {
    const k = tokens / 1_000
    return k % 1 === 0 ? `${k}K` : `${k.toFixed(1).replace(/\.0$/, '')}K`
  }
  return `${tokens}`
}

/**
 * 查阅官方模型定义（上下文长度、能力标签、官方参考价）。
 * 匹配策略：
 * 1. 精确小写模型名
 * 2. 去除 "models/" 前缀
 * 3. 去除日期后缀（如 -20241022、-2025-08-07）
 * 4. 前缀与系列兜底
 */
export function lookupOfficialModel(modelName: string): OfficialModelSpec | null {
  const norm = modelName.toLowerCase().trim()
  if (!norm) return null

  if (officialModels[norm]) {
    return officialModels[norm]
  }

  const cleaned = norm.replace(/^models\//, '')
  if (officialModels[cleaned]) {
    return officialModels[cleaned]
  }

  // 去除日期版本号后缀
  const noDate = cleaned
    .replace(/-\d{4}-\d{2}-\d{2}$/, '')
    .replace(/-\d{8}$/, '')
    .replace(/-v\d+:\d+$/, '')

  if (officialModels[noDate]) {
    return officialModels[noDate]
  }

  // 家族/前缀匹配
  for (const [key, spec] of Object.entries(officialModels)) {
    if (cleaned.startsWith(key) || key.startsWith(cleaned)) {
      return spec
    }
  }

  // DeepSeek 默认归入 flash
  if (cleaned.startsWith('deepseek-')) {
    if (cleaned.includes('pro')) {
      return officialModels['deepseek-v4-pro'] ?? null
    }
    return officialModels['deepseek-v4-flash'] ?? null
  }

  // Claude 默认归入 sonnet-4
  if (cleaned.startsWith('claude-')) {
    if (cleaned.includes('opus')) {
      return officialModels['claude-opus-4.5'] ?? null
    }
    if (cleaned.includes('haiku')) {
      return officialModels['claude-3-5-haiku'] ?? null
    }
    return officialModels['claude-sonnet-4'] ?? null
  }

  // Qwen 默认
  if (cleaned.startsWith('qwen-') || cleaned.startsWith('qwen')) {
    return officialModels['qwen-2.5-72b'] ?? null
  }

  // GLM 默认
  if (cleaned.startsWith('glm-')) {
    return officialModels['glm-4.7'] ?? null
  }

  // Kimi 默认
  if (cleaned.startsWith('kimi-') || cleaned.startsWith('moonshot-')) {
    return officialModels['kimi-k2.5'] ?? null
  }

  return null
}

/**
 * 确定模型的上下文尺寸，以官方资料为准，未知模型采用名称推断保底
 */
export function resolveContextWindow(modelName: string): { tokens: number; label: string } {
  const official = lookupOfficialModel(modelName)
  if (official && official.context_tokens && official.context_tokens > 0) {
    return {
      tokens: official.context_tokens,
      label: formatContextTokens(official.context_tokens),
    }
  }

  const name = modelName.toLowerCase()
  if (name.includes('gemini-1.5-pro') || name.includes('gemini-2.0-pro') || name.includes('gemini-1.5-flash')) {
    return { tokens: 2_000_000, label: '2M' }
  }
  if (name.includes('gemini')) {
    return { tokens: 1_000_000, label: '1M' }
  }
  if (name.includes('claude-3') || name.includes('claude-2')) {
    return { tokens: 200_000, label: '200K' }
  }
  if (name.includes('deepseek')) {
    return { tokens: 1_048_576, label: '1M' }
  }
  if (name.includes('llama-3.1') || name.includes('llama-3.2') || name.includes('llama-3.3')) {
    return { tokens: 128_000, label: '128K' }
  }

  const kMatch = name.match(/(\d+)k\b/)
  if (kMatch) {
    const k = parseInt(kMatch[1], 10)
    return { tokens: k * 1000, label: `${k}K` }
  }
  const mMatch = name.match(/(\d+)m\b/)
  if (mMatch) {
    const m = parseInt(mMatch[1], 10)
    return { tokens: m * 1_000_000, label: `${m}M` }
  }

  return { tokens: 128_000, label: '128K' }
}

/**
 * 确定模型的能力特征（视觉、工具调用、深度思考、语音、生图、向量等），以官方资料为准，
 * 如果官方无覆盖，则根据名称与渠道定价推断。
 */
export function resolveCapabilities(modelName: string, pricing?: UserSupportedModelPricing | null): ModelCapability[] {
  const caps = new Set<ModelCapability>()
  const official = lookupOfficialModel(modelName)

  if (official && official.capabilities && official.capabilities.length > 0) {
    for (const c of official.capabilities) {
      caps.add(c)
    }
  }

  const name = modelName.toLowerCase()

  // 渠道配置中包含生图或按次计费
  if (
    pricing?.billing_mode === 'image' ||
    name.includes('dall-e') ||
    name.includes('midjourney') ||
    name.includes('flux') ||
    name.includes('stable-diffusion') ||
    name.includes('image-generation')
  ) {
    caps.add('image_gen')
  }

  // 渠道中配置了图片输入价格
  if (pricing?.image_input_price != null && pricing.image_input_price > 0) {
    caps.add('vision')
  }

  // 渠道中配置了推理倍率
  if (pricing?.max_reasoning_effort_multiplier != null && pricing.max_reasoning_effort_multiplier > 0) {
    caps.add('reasoning')
  }

  // 如果没有命中官方能力，通过名称关键词推断
  if (caps.size === 0) {
    if (name.includes('embed') || name.includes('bge-') || name.includes('text-embedding')) {
      caps.add('embedding')
    }
    if (name.includes('vision') || name.includes('vl') || name.includes('4o') || name.includes('gemini') || name.includes('claude-3')) {
      caps.add('vision')
    }
    if (name.includes('gpt-') || name.includes('claude-') || name.includes('gemini-') || name.includes('deepseek') || name.includes('qwen') || name.includes('glm')) {
      caps.add('tool_call')
    }
    if (name.includes('o1') || name.includes('o3') || name.includes('r1') || name.includes('thinking') || name.includes('reasoner')) {
      caps.add('reasoning')
    }
    if (name.includes('whisper') || name.includes('tts') || name.includes('audio') || name.includes('realtime')) {
      caps.add('audio')
    }
  }

  return Array.from(caps)
}

/**
 * 判定一个 Pricing 是否为有效自定义定价（非空且至少有一项非零价格）
 */
export function hasCustomPricing(pricing?: UserSupportedModelPricing | null): boolean {
  if (!pricing) return false

  if (
    (pricing.input_price != null && pricing.input_price > 0) ||
    (pricing.output_price != null && pricing.output_price > 0) ||
    (pricing.cache_write_price != null && pricing.cache_write_price > 0) ||
    (pricing.cache_read_price != null && pricing.cache_read_price > 0) ||
    (pricing.per_request_price != null && pricing.per_request_price > 0) ||
    (pricing.image_input_price != null && pricing.image_input_price > 0) ||
    (pricing.image_output_price != null && pricing.image_output_price > 0)
  ) {
    return true
  }

  if (pricing.intervals && pricing.intervals.length > 0) {
    return pricing.intervals.some(
      (iv) =>
        (iv.input_price != null && iv.input_price > 0) ||
        (iv.output_price != null && iv.output_price > 0) ||
        (iv.cache_write_price != null && iv.cache_write_price > 0) ||
        (iv.cache_read_price != null && iv.cache_read_price > 0) ||
        (iv.per_request_price != null && iv.per_request_price > 0),
    )
  }

  return false
}

/**
 * 获取模型生效定价：
 * 如果渠道设置了有效自定义定价，则优先使用；
 * 如果没有，则使用官方价格作为兜底回退定价。
 */
export function resolveEffectivePricing(
  modelName: string,
  channelPricing?: UserSupportedModelPricing | null,
): { pricing: UserSupportedModelPricing; isOfficialFallback: boolean } {
  if (hasCustomPricing(channelPricing)) {
    return {
      pricing: channelPricing!,
      isOfficialFallback: false,
    }
  }

  const official = lookupOfficialModel(modelName)
  if (official) {
    const fallbackPricing: UserSupportedModelPricing = {
      billing_mode: official.billing_mode || (channelPricing?.billing_mode ?? 'token'),
      input_price: official.input_price ?? channelPricing?.input_price ?? null,
      output_price: official.output_price ?? channelPricing?.output_price ?? null,
      cache_write_price: official.cache_write_price ?? channelPricing?.cache_write_price ?? null,
      cache_read_price: official.cache_read_price ?? channelPricing?.cache_read_price ?? null,
      per_request_price: official.per_request_price ?? channelPricing?.per_request_price ?? null,
      image_input_price: channelPricing?.image_input_price ?? null,
      image_output_price: channelPricing?.image_output_price ?? null,
      intervals: channelPricing?.intervals ?? [],
      max_reasoning_effort_multiplier: channelPricing?.max_reasoning_effort_multiplier ?? null,
    }
    return {
      pricing: fallbackPricing,
      isOfficialFallback: true,
    }
  }

  return {
    pricing: channelPricing || {
      billing_mode: 'token',
      input_price: null,
      output_price: null,
      cache_write_price: null,
      cache_read_price: null,
      per_request_price: null,
      image_input_price: null,
      image_output_price: null,
      intervals: [],
    },
    isOfficialFallback: false,
  }
}
