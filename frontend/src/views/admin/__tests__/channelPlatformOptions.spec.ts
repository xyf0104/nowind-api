import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'

describe('Composite channel platform options', () => {
  it('includes the CN concrete providers for pricing and model mapping', () => {
    const source = readFileSync(resolve('src/views/admin/ChannelsView.vue'), 'utf8')
    const declaration = source.match(/const compositePlatforms:[^=]+=[^\n]+/)?.[0]

    const concretePlatforms = CONCRETE_PLATFORM_OPTIONS.map(({ value }) => value)

    expect(concretePlatforms).toEqual(expect.arrayContaining(['kimi', 'zhipu', 'deepseek']))
    expect(declaration).toContain('[...platformOrder]')
  })
})
