import { describe, expect, it } from 'vitest'

import { sortGroups, type SortableGroup } from '../groupSorting'

const group = (overrides: Partial<SortableGroup> = {}): SortableGroup => ({
  id: 1,
  name: 'Group',
  platform: 'openai',
  rate_multiplier: 1,
  is_exclusive: false,
  sort_order: 0,
  ...overrides,
})

describe('sortGroups', () => {
  it('orders public groups by platform and effective rate', () => {
    const groups = [
      group({ id: 1, name: 'Other', platform: 'composite', rate_multiplier: 0.01 }),
      group({ id: 2, name: 'Grok', platform: 'grok', rate_multiplier: 0.01 }),
      group({ id: 3, name: 'Antigravity', platform: 'antigravity', rate_multiplier: 0.01 }),
      group({ id: 4, name: 'Gemini', platform: 'gemini', rate_multiplier: 0.01 }),
      group({ id: 5, name: 'Claude', platform: 'anthropic', rate_multiplier: 0.01 }),
      group({ id: 6, name: 'OpenAI 0.2', platform: 'openai', rate_multiplier: 0.2 }),
      group({ id: 7, name: 'OpenAI 0.1', platform: 'openai', rate_multiplier: 0.1 }),
    ]

    expect(sortGroups(groups).map(({ id }) => id)).toEqual([7, 6, 5, 4, 3, 2, 1])
  })

  it('places every exclusive group after public groups and repeats platform ordering inside it', () => {
    const groups = [
      group({ id: 1, platform: 'openai', is_exclusive: true }),
      group({ id: 2, platform: 'grok', is_exclusive: false }),
      group({ id: 3, platform: 'anthropic', is_exclusive: true }),
      group({ id: 4, platform: 'openai', is_exclusive: false }),
    ]

    expect(sortGroups(groups).map(({ id }) => id)).toEqual([4, 2, 1, 3])
  })

  it('treats claude and anthropic as one platform family before comparing rates', () => {
    const groups = [
      group({ id: 1, name: 'Anthropic 0.5', platform: 'anthropic', rate_multiplier: 0.5 }),
      group({ id: 2, name: 'Claude 0.1', platform: 'claude', rate_multiplier: 0.1 }),
    ]

    expect(sortGroups(groups).map(({ id }) => id)).toEqual([2, 1])
  })

  it('supports a caller-provided effective rate and leaves the source array unchanged', () => {
    const groups = [
      group({ id: 1, name: 'Base 0.1', rate_multiplier: 0.1 }),
      group({ id: 2, name: 'Custom 0.2', rate_multiplier: 0.2 }),
    ]
    const effectiveRates: Record<number, number> = { 1: 0.3, 2: 0.05 }

    const sorted = sortGroups(groups, {
      getEffectiveRate: (item) => effectiveRates[item.id],
    })

    expect(sorted.map(({ id }) => id)).toEqual([2, 1])
    expect(groups.map(({ id }) => id)).toEqual([1, 2])
  })

  it('uses sort_order, name, and id as deterministic tie breakers', () => {
    const groups = [
      group({ id: 4, name: 'Beta', sort_order: 20 }),
      group({ id: 3, name: 'Alpha', sort_order: 20 }),
      group({ id: 2, name: 'Same', sort_order: 10 }),
      group({ id: 1, name: 'Same', sort_order: 10 }),
    ]

    expect(sortGroups(groups).map(({ id }) => id)).toEqual([1, 2, 3, 4])
  })
})
