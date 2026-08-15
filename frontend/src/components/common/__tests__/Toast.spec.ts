import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import Toast from '@/components/common/Toast.vue'
import { useAppStore } from '@/stores/app'

describe('Toast', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('uses the opaque floating surface class above dialogs and console controls', async () => {
    const wrapper = mount(Toast)
    const appStore = useAppStore()

    appStore.showSuccess('ready', 60_000)
    await wrapper.vm.$nextTick()

    const toast = document.body.querySelector<HTMLElement>('.toast')
    const stack = toast?.parentElement
    expect(toast).not.toBeNull()
    expect(toast?.classList.contains('bg-white')).toBe(true)
    expect(toast?.classList.contains('dark:bg-dark-800')).toBe(true)
    expect(stack?.classList.contains('z-[100000300]')).toBe(true)

    wrapper.unmount()
  })
})
