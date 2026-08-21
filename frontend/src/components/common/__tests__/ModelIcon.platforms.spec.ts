import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ModelIcon from '../ModelIcon.vue'

describe('ModelIcon platform brands', () => {
  const platforms = [
    ['anthropic', '#D97757'],
    ['openai', '#10A37F'],
    ['gemini', '#3186FF'],
    ['antigravity', '#4285F4'],
    ['grok', '#000000'],
    ['kimi', '#1783FF'],
    ['zhipu', '#3859FF'],
    ['deepseek', '#4D6BFE']
  ] as const

  it.each(platforms)('renders the %s official brand mark in color', (platform, color) => {
    const wrapper = mount(ModelIcon, { props: { model: platform } })

    expect(wrapper.find('svg').exists()).toBe(true)
    expect(wrapper.find('.model-icon-fallback').exists()).toBe(false)
    expect(wrapper.find('path').attributes('fill')).toBe(color)
  })
})
