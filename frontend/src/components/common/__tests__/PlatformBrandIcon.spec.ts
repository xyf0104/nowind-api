import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PlatformBrandIcon from '../PlatformBrandIcon.vue'
import type { GroupPlatform } from '@/types'

describe('PlatformBrandIcon', () => {
  const platforms: GroupPlatform[] = [
    'anthropic',
    'openai',
    'gemini',
    'antigravity',
    'grok',
    'kimi',
    'zhipu',
    'deepseek'
  ]

  it.each(platforms)('renders a stable colored avatar for %s', (platform) => {
    const wrapper = mount(PlatformBrandIcon, { props: { platform } })
    const avatar = wrapper.get(`[data-platform-brand="${platform}"]`)

    expect(avatar.attributes('style')).toContain('background:')
    expect(avatar.find('svg').exists()).toBe(true)
    expect(avatar.find('.model-icon-fallback').exists()).toBe(false)
  })

  it('keeps the Kimi mark blue and white', () => {
    const paths = mount(PlatformBrandIcon, { props: { platform: 'kimi' } }).findAll('path')

    expect(paths[0]?.attributes('fill')).toBe('#1783FF')
    expect(paths[1]?.attributes('fill')).toBe('#FFFFFF')
  })

  it.each(['gemini', 'antigravity'] as const)('uses a multicolor field for %s', (platform) => {
    const style = mount(PlatformBrandIcon, { props: { platform } }).attributes('style')

    expect(style).toContain('linear-gradient')
  })
})
