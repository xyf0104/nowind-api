export interface SortableGroup {
  id: number
  name: string
  platform: string
  rate_multiplier?: number | null
  is_exclusive?: boolean
  sort_order?: number | null
}

interface GroupSortOptions<T extends SortableGroup> {
  getEffectiveRate?: (group: T) => number | null | undefined
}

const PLATFORM_ORDER: Record<string, number> = {
  openai: 0,
  anthropic: 1,
  claude: 1,
  gemini: 2,
  antigravity: 3,
  grok: 4,
}

const PLATFORM_FAMILY: Record<string, string> = {
  openai: 'openai',
  anthropic: 'claude',
  claude: 'claude',
  gemini: 'gemini',
  antigravity: 'antigravity',
  grok: 'grok',
}

const nameCollator = new Intl.Collator('zh-CN', {
  numeric: true,
  sensitivity: 'base',
})

const platformRank = (platform: string) =>
  PLATFORM_ORDER[platform.trim().toLowerCase()] ?? Number.MAX_SAFE_INTEGER

const normalizedPlatform = (platform: string) => {
  const normalized = platform.trim().toLowerCase()
  return PLATFORM_FAMILY[normalized] ?? normalized
}

const sortableNumber = (value: number | null | undefined) =>
  typeof value === 'number' && Number.isFinite(value) ? value : Number.POSITIVE_INFINITY

const compareSortableNumbers = (left: number, right: number) => {
  if (left === right) return 0
  return left < right ? -1 : 1
}

export const compareGroups = <T extends SortableGroup>(
  left: T,
  right: T,
  options: GroupSortOptions<T> = {},
) => {
  const exclusiveDifference = Number(Boolean(left.is_exclusive)) - Number(Boolean(right.is_exclusive))
  if (exclusiveDifference !== 0) return exclusiveDifference

  const platformDifference = platformRank(left.platform) - platformRank(right.platform)
  if (platformDifference !== 0) return platformDifference

  const platformNameDifference = nameCollator.compare(
    normalizedPlatform(left.platform),
    normalizedPlatform(right.platform),
  )
  if (platformNameDifference !== 0) return platformNameDifference

  const getRate = options.getEffectiveRate ?? ((group: T) => group.rate_multiplier)
  const rateDifference = compareSortableNumbers(
    sortableNumber(getRate(left)),
    sortableNumber(getRate(right)),
  )
  if (rateDifference !== 0) return rateDifference

  const sortOrderDifference = compareSortableNumbers(
    sortableNumber(left.sort_order),
    sortableNumber(right.sort_order),
  )
  if (sortOrderDifference !== 0) return sortOrderDifference

  const nameDifference = nameCollator.compare(left.name, right.name)
  if (nameDifference !== 0) return nameDifference

  return left.id - right.id
}

export const sortGroups = <T extends SortableGroup>(
  groups: readonly T[],
  options: GroupSortOptions<T> = {},
) => [...groups].sort((left, right) => compareGroups(left, right, options))
