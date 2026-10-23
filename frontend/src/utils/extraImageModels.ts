export function parseExtraImageModels(value: string): string[] {
  return [...new Set(value.split(/[\n,]/).map(entry => entry.trim()).filter(Boolean))]
}

export function readExtraImageModels(value: unknown): string {
  return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === 'string').join('\n') : ''
}
