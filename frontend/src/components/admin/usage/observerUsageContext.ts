import type { InjectionKey } from 'vue'

// Shared usage components default to admin mode outside the observer's own page.
export const observerUsageContext: InjectionKey<boolean> = Symbol('observer-own-usage')
