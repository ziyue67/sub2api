import type { ModelCapability } from '../types'
import type { UserSupportedModelPricing } from '@/api/channels'

/**
 * Infer context window tokens from model name or pricing details.
 */
export function inferContextWindow(modelName: string): { tokens: number; label: string } {
  const name = modelName.toLowerCase()

  // Specific common prefixes / patterns
  if (name.includes('gemini-1.5-pro') || name.includes('gemini-2.0-pro') || name.includes('gemini-1.5-flash')) {
    return { tokens: 2_000_000, label: '2M' }
  }
  if (name.includes('gemini')) {
    return { tokens: 1_000_000, label: '1M' }
  }
  if (name.includes('claude-3') || name.includes('claude-2')) {
    return { tokens: 200_000, label: '200K' }
  }
  if (name.includes('gpt-4o') || name.includes('gpt-4-turbo') || name.includes('o1') || name.includes('o3')) {
    return { tokens: 128_000, label: '128K' }
  }
  if (name.includes('deepseek')) {
    return { tokens: 128_000, label: '128K' }
  }
  if (name.includes('qwen-2.5') || name.includes('qwen2.5')) {
    return { tokens: 128_000, label: '128K' }
  }
  if (name.includes('qwen')) {
    return { tokens: 32_000, label: '32K' }
  }
  if (name.includes('glm-4') || name.includes('glm-3')) {
    return { tokens: 128_000, label: '128K' }
  }
  if (name.includes('moonshot') || name.includes('kimi')) {
    if (name.includes('128k')) return { tokens: 128_000, label: '128K' }
    if (name.includes('32k')) return { tokens: 32_000, label: '32K' }
    if (name.includes('8k')) return { tokens: 8_000, label: '8K' }
    return { tokens: 128_000, label: '128K' }
  }
  if (name.includes('llama-3.1') || name.includes('llama-3.2') || name.includes('llama-3.3')) {
    return { tokens: 128_000, label: '128K' }
  }
  if (name.includes('llama-3')) {
    return { tokens: 8_000, label: '8K' }
  }
  if (name.includes('mistral-large') || name.includes('codestral')) {
    return { tokens: 128_000, label: '128K' }
  }
  if (name.includes('grok-2') || name.includes('grok-beta')) {
    return { tokens: 128_000, label: '128K' }
  }

  // Regex check for explicit numbers like 32k, 128k, 1m, 200k
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

  // Default fallback
  return { tokens: 128_000, label: '128K' }
}

/**
 * Infer capabilities (vision, tools, reasoning, audio, etc.) from model name and pricing fields.
 */
export function inferCapabilities(modelName: string, pricing?: UserSupportedModelPricing | null): ModelCapability[] {
  const caps: Set<ModelCapability> = new Set()
  const name = modelName.toLowerCase()

  // Image gen
  if (
    name.includes('dall-e') ||
    name.includes('midjourney') ||
    name.includes('flux') ||
    name.includes('stable-diffusion') ||
    pricing?.billing_mode === 'image'
  ) {
    caps.add('image_gen')
  }

  // Embedding
  if (name.includes('embed') || name.includes('bge-') || name.includes('text-embedding')) {
    caps.add('embedding')
  }

  // Vision / multimodal
  if (
    name.includes('vision') ||
    name.includes('vl') ||
    name.includes('4o') ||
    name.includes('gemini') ||
    name.includes('claude-3') ||
    name.includes('grok-2-vision') ||
    name.includes('qwen-vl') ||
    (pricing?.image_input_price != null && pricing.image_input_price > 0)
  ) {
    caps.add('vision')
  }

  // Tool / Function call
  if (
    name.includes('gpt-4') ||
    name.includes('gpt-3.5') ||
    name.includes('claude') ||
    name.includes('gemini') ||
    name.includes('deepseek') ||
    name.includes('qwen') ||
    name.includes('mistral') ||
    name.includes('glm-4')
  ) {
    caps.add('tool_call')
  }

  // Reasoning / Thinking
  if (
    name.includes('o1') ||
    name.includes('o3') ||
    name.includes('r1') ||
    name.includes('thinking') ||
    name.includes('reasoner') ||
    (pricing?.max_reasoning_effort_multiplier != null && pricing.max_reasoning_effort_multiplier > 0)
  ) {
    caps.add('reasoning')
  }

  // Audio / Speech
  if (name.includes('whisper') || name.includes('tts') || name.includes('audio') || name.includes('realtime')) {
    caps.add('audio')
  }

  return Array.from(caps)
}

export function capabilityMeta(cap: ModelCapability): { label: string; icon: string; description: string } {
  switch (cap) {
    case 'vision':
      return { label: '视觉识别', icon: 'eye', description: '支持图像与文档视觉多模态输入' }
    case 'tool_call':
      return { label: '工具调用', icon: 'wrench', description: '支持 Function Calling 和外部工具编排' }
    case 'reasoning':
      return { label: '深度思考', icon: 'brain', description: '支持链式思维强化推理 / 深度思考' }
    case 'audio':
      return { label: '语音交互', icon: 'sparkles', description: '支持音频/语音处理' }
    case 'image_gen':
      return { label: '图像生成', icon: 'image', description: '支持 AI 文生图与图像生成' }
    case 'embedding':
      return { label: '向量检索', icon: 'database', description: '支持文本向量化 Embedding' }
  }
}
