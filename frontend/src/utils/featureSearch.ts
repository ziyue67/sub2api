export interface SearchNavItem {
  path: string
  label: string
  keywords?: string
  children?: SearchNavItem[]
  expandOnly?: boolean
}

export interface FeatureSearchEntry {
  path: string
  label: string
  group: string
  keywords: string
}

const normalize = (value: string) => value.normalize('NFKC').toLowerCase().replace(/[_/.-]+/g, ' ').trim()

// The caller supplies the already-filtered sidebar. Never index all router records:
// doing so would surface features hidden by permissions, feature flags or simple mode.
export function buildFeatureSearchEntries(
  items: SearchNavItem[],
  routeKeywords: (path: string) => string = () => ''
): FeatureSearchEntry[] {
  const entries: FeatureSearchEntry[] = []
  const seen = new Set<string>()
  function visit(nodes: SearchNavItem[], parents: string[]) {
    for (const item of nodes) {
      if (!item.expandOnly && !seen.has(item.path)) {
        seen.add(item.path)
        entries.push({
          path: item.path,
          label: item.label,
          group: parents.join(' / '),
          keywords: normalize([item.label, item.keywords, ...parents, item.path, routeKeywords(item.path)].join(' '))
        })
      }
      if (item.children) visit(item.children, [...parents, item.label])
    }
  }
  visit(items, [])
  return entries
}

export function searchFeatures(entries: FeatureSearchEntry[], query: string): FeatureSearchEntry[] {
  const term = normalize(query)
  if (!term) return entries
  const tokens = term.split(/\s+/)
  const rank = (entry: FeatureSearchEntry) => {
    const label = normalize(entry.label)
    return label === term ? 0 : label.startsWith(term) ? 1 : label.includes(term) ? 2 : 3
  }
  return entries.filter(entry => tokens.every(token => entry.keywords.includes(token)))
    .sort((a, b) => rank(a) - rank(b))
}
