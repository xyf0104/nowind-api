import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { defineComponent } from 'vue'
import { createPinia } from 'pinia'
import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import AppLayout from '../AppLayout.vue'

vi.mock('@/composables/useOnboardingTour', () => ({
  useOnboardingTour: () => ({ replayTour: vi.fn() }),
}))

const currentDir = dirname(fileURLToPath(import.meta.url))
const appPath = resolve(currentDir, '../../../App.vue')
const layoutPath = resolve(currentDir, '../AppLayout.vue')
const appSource = readFileSync(appPath, 'utf8')
const layoutSource = readFileSync(layoutPath, 'utf8')

describe('persistent console layout', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('keeps a single application shell around normal console routes', () => {
    expect(appSource).toContain('<AppLayout v-if="usesPersistentAppLayout">')
    expect(appSource).toContain('<component :is="Component" />')
  })

  it('renders legacy per-view layouts as content-only children', () => {
    expect(layoutSource).toContain('<slot v-if="!isRootLayout" />')
    expect(layoutSource).toContain("const hasParentLayout = inject<boolean>(APP_LAYOUT_CONTEXT, false)")
    expect(layoutSource).toContain('if (isRootLayout) {')
    expect(layoutSource).toContain('provide(APP_LAYOUT_CONTEXT, true)')
  })

  it('mounts the shell once when a legacy view still wraps itself in AppLayout', () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(null)

    const LegacyView = defineComponent({
      components: { AppLayout },
      template: '<AppLayout><section data-test="legacy-page">Legacy view</section></AppLayout>',
    })
    const Root = defineComponent({
      components: { AppLayout, LegacyView },
      template: '<AppLayout><LegacyView /></AppLayout>',
    })

    const wrapper = mount(Root, {
      global: {
        plugins: [createPinia()],
        stubs: {
          AppSidebar: true,
          AppHeader: true,
          DarkVideoBackground: true,
        },
      },
    })

    expect(wrapper.findAll('.app-layout')).toHaveLength(1)
    expect(wrapper.findAll('main')).toHaveLength(1)
    expect(wrapper.get('[data-test="legacy-page"]').text()).toBe('Legacy view')
  })

  it('keeps the operational dashboard out of the persistent shell because it owns fullscreen mode', () => {
    const routeNamesBlock = appSource.match(/const APP_LAYOUT_ROUTE_NAMES = new Set\(\[([\s\S]*?)\]\)/)?.[1] ?? ''
    expect(routeNamesBlock).not.toContain("'AdminOps'")
  })
})
