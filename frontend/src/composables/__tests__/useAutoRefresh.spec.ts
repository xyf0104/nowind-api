import { defineComponent, onMounted } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import { useAutoRefresh } from '../useAutoRefresh'

const storageKey = 'auto-refresh-test'

function mountHarness(onRefresh: () => void) {
  return mount(defineComponent({
    setup() {
      const autoRefresh = useAutoRefresh({
        storageKey,
        intervals: [5] as const,
        defaultInterval: 5,
        defaultEnabled: true,
        onRefresh,
      })
      onMounted(() => {
        if (!autoRefresh.enabled.value) return
        autoRefresh.resetCountdown()
        autoRefresh.start()
      })
      return { enabled: autoRefresh.enabled }
    },
    template: '<div>{{ enabled }}</div>',
  }))
}

describe('useAutoRefresh enabled preference', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    localStorage.clear()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('preserves an explicitly disabled saved preference over an enabled default', async () => {
    localStorage.setItem(storageKey, JSON.stringify({ enabled: false, interval_seconds: 5 }))
    const onRefresh = vi.fn()
    const wrapper = mountHarness(onRefresh)

    expect(wrapper.text()).toBe('false')
    await vi.advanceTimersByTimeAsync(6000)
    expect(onRefresh).not.toHaveBeenCalled()
    expect(JSON.parse(localStorage.getItem(storageKey) || '{}').enabled).toBe(false)

    wrapper.unmount()
  })

  it('uses the enabled default only when no saved preference exists', async () => {
    const onRefresh = vi.fn()
    const wrapper = mountHarness(onRefresh)

    expect(wrapper.text()).toBe('true')
    await vi.advanceTimersByTimeAsync(6000)
    expect(onRefresh).toHaveBeenCalledTimes(1)

    wrapper.unmount()
  })
})
