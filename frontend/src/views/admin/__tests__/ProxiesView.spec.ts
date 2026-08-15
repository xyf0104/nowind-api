import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'

import type { Proxy } from '@/types'
import ProxiesView from '../ProxiesView.vue'

const { listProxies, getAllWithCount, copyToClipboard } = vi.hoisted(() => ({
  listProxies: vi.fn(),
  getAllWithCount: vi.fn(),
  copyToClipboard: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    proxies: {
      list: listProxies,
      getAllWithCount
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn()
  })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard })
}))

vi.mock('@/composables/useSwipeSelect', () => ({
  useSwipeSelect: vi.fn()
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const proxy: Proxy = {
  id: 11,
  name: 'Primary Proxy',
  protocol: 'http',
  host: 'proxy.example',
  port: 8080,
  username: 'alice',
  password: 'secret',
  status: 'active',
  expires_at: null,
  fallback_mode: 'none',
  expiry_warn_days: 7,
  created_at: '2026-08-16T00:00:00Z',
  updated_at: '2026-08-16T00:00:00Z'
}

const DataTableStub = {
  props: ['data'],
  template: `
    <div data-test="proxy-table-overflow" style="overflow: hidden">
      <div v-for="row in data" :key="row.id">
        <slot name="cell-address" :row="row" />
      </div>
    </div>
  `
}

const originalInnerWidth = window.innerWidth
const originalInnerHeight = window.innerHeight

function setViewportSize(width: number, height: number) {
  Object.defineProperties(window, {
    innerWidth: { configurable: true, value: width },
    innerHeight: { configurable: true, value: height }
  })
}

function makeRect(left: number, top: number, width: number, height: number): DOMRect {
  return {
    x: left,
    y: top,
    left,
    top,
    right: left + width,
    bottom: top + height,
    width,
    height,
    toJSON: () => ({})
  } as DOMRect
}

describe('admin ProxiesView copy-format menu', () => {
  beforeEach(() => {
    localStorage.clear()
    listProxies.mockReset()
    getAllWithCount.mockReset()
    copyToClipboard.mockReset()

    listProxies.mockResolvedValue({
      items: [proxy],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1
    })
    getAllWithCount.mockResolvedValue([proxy])
  })

  afterEach(() => {
    document.body.innerHTML = ''
    setViewportSize(originalInnerWidth, originalInnerHeight)
    vi.restoreAllMocks()
  })

  it('portals, flips, and repositions the right-click menu without changing copy behavior', async () => {
    setViewportSize(320, 240)
    const wrapper = mount(ProxiesView, {
      attachTo: document.body,
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: {
            template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
          },
          DataTable: DataTableStub,
          Pagination: true,
          BaseDialog: true,
          ConfirmDialog: true,
          EmptyState: true,
          ImportDataModal: true,
          SoftRouterProxyPanel: true,
          Select: true,
          Icon: true,
          PlatformTypeBadge: true,
          Teleport: false
        }
      }
    })

    await flushPromises()

    const trigger = wrapper.get('[data-test="copy-format-trigger-11"]')
    let triggerRect = makeRect(290, 210, 18, 18)
    vi.spyOn(trigger.element, 'getBoundingClientRect').mockImplementation(() => triggerRect)

    await trigger.trigger('contextmenu')
    await flushPromises()

    const menu = document.body.querySelector<HTMLElement>('[data-test="copy-format-menu-11"]')
    expect(menu).not.toBeNull()
    expect(menu?.parentElement).toBe(document.body)
    expect(menu?.classList.contains('fixed')).toBe(true)
    expect(menu?.className).toContain('z-[100000020]')

    Object.defineProperties(menu!, {
      offsetWidth: { configurable: true, value: 220 },
      scrollWidth: { configurable: true, value: 220 },
      offsetHeight: { configurable: true, value: 96 },
      scrollHeight: { configurable: true, value: 96 }
    })
    window.dispatchEvent(new Event('resize'))
    await nextTick()

    expect(menu?.style.left).toBe('92px')
    expect(menu?.style.top).toBe('108px')
    expect(menu?.style.maxHeight).toBe('196px')
    expect(document.activeElement).toBe(menu?.querySelector('[role="menuitem"]'))

    triggerRect = makeRect(4, 20, 18, 18)
    window.dispatchEvent(new Event('scroll'))
    await nextTick()
    expect(menu?.style.left).toBe('8px')
    expect(menu?.style.top).toBe('44px')

    setViewportSize(200, 240)
    triggerRect = makeRect(190, -40, 18, 18)
    window.dispatchEvent(new Event('resize'))
    await nextTick()
    expect(menu?.style.left).toBe('8px')
    expect(menu?.style.top).toBe('8px')
    expect(menu?.style.width).toBe('184px')

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flushPromises()
    expect(document.body.querySelector('[data-test="copy-format-menu-11"]')).toBeNull()
    expect(document.activeElement).toBe(trigger.element)

    await trigger.trigger('contextmenu')
    await flushPromises()
    document.body.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await nextTick()
    expect(document.body.querySelector('[data-test="copy-format-menu-11"]')).toBeNull()

    await trigger.trigger('contextmenu')
    await flushPromises()
    document.body.querySelector<HTMLButtonElement>('[data-test="copy-format-menu-11"] [role="menuitem"]')?.click()
    await nextTick()
    expect(copyToClipboard).toHaveBeenCalledWith(
      'http://alice:secret@proxy.example:8080',
      'admin.proxies.urlCopied'
    )
    expect(document.body.querySelector('[data-test="copy-format-menu-11"]')).toBeNull()

    wrapper.unmount()
  })
})
