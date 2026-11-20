import { ref } from 'vue'
import { apiClient } from '@/api/client'
import type { PelicanPersonalQuestion, PelicanTestConfig } from '@/types'

// Shared within this browser tab so opening another account reuses the round.
// Never reuse another administrator's round or persist credentials.
export const pelicanQuestionRound = ref<PelicanTestConfig | null>(null)
const personalRound = ref<PelicanTestConfig | null>(null)
let owner: number | null = null
let personalOwner: number | null = null
export async function preparePelicanQuestionRound(userId: number | null, signal?: AbortSignal, personal = false, previousId?: string) {
  const path = personal ? '/admin/accounts/pelican-personal-question' : '/admin/accounts/pelican-question'
  const { data } = await apiClient.get<PelicanTestConfig>(path, personal ? { signal, params: { previous_id: previousId } } : { signal })
  if (signal?.aborted) throw new DOMException('Aborted', 'AbortError')
  if (!data.pelican_question || !data.actual_prompt) throw new Error('Invalid question snapshot')
  if (personal) {
    if (!data.pelican_question.proof) throw new Error('Invalid personal question snapshot')
    personalOwner = userId
    personalRound.value = data
  } else {
    owner = userId
    pelicanQuestionRound.value = data
  }
  return data
}
export function currentPelicanQuestionRound(userId: number | null, personal = false) {
  if (personal) return personalOwner === userId ? personalRound.value : null
  return owner === userId ? pelicanQuestionRound.value : null
}

export function clearPersonalPelicanQuestionRound(userId: number | null) {
  if (userId === personalOwner) personalRound.value = null
}

export async function loadPelicanPersonalBank(signal?: AbortSignal) {
  const { data } = await apiClient.get<PelicanPersonalQuestion[]>('/admin/accounts/pelican-personal-questions', { signal })
  return data
}

export async function savePelicanPersonalBank(items: PelicanPersonalQuestion[], signal?: AbortSignal) {
  const { data } = await apiClient.put<PelicanPersonalQuestion[]>('/admin/accounts/pelican-personal-questions', { items }, { signal })
  return data
}
