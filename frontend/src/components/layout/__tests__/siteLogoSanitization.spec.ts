import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const dir = dirname(fileURLToPath(import.meta.url))
const sidebarSource = readFileSync(resolve(dir, '../AppSidebar.vue'), 'utf8')
const homeViewSource = readFileSync(resolve(dir, '../../../views/HomeView.vue'), 'utf8')
const keyUsageViewSource = readFileSync(resolve(dir, '../../../views/KeyUsageView.vue'), 'utf8')

describe('site_logo sanitization', () => {
  it('AppSidebar imports sanitizeUrl and applies it to siteLogo', () => {
    expect(sidebarSource).toContain("import { sanitizeUrl } from '@/utils/url'")
    expect(sidebarSource).toContain('sanitizeUrl(appStore.siteLogo')
  })

  it('HomeView uses the bundled theme logos and does not render an unsanitized siteLogo URL', () => {
    expect(homeViewSource).not.toContain('cachedPublicSettings?.site_logo')
    expect(homeViewSource).not.toContain(':src="siteLogo')
    expect(homeViewSource).toContain('/brand/xiass-mark-light.png')
    expect(homeViewSource).toContain('/brand/xiass-mark-dark.png')
  })

  it('KeyUsageView applies sanitizeUrl to siteLogo', () => {
    expect(keyUsageViewSource).toContain('sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo')
  })

  it('all siteLogo consumers pass allowRelative and allowDataUrl options', () => {
    for (const src of [sidebarSource, keyUsageViewSource]) {
      expect(src).toContain('allowRelative: true')
      expect(src).toContain('allowDataUrl: true')
    }
  })
})

describe('HomeView runtime wiring', () => {
  it('registers the provider icon component used by the home page', () => {
    expect(homeViewSource).toContain("import BrandIcon from '@/components/icons/BrandIcon.vue'")
  })

  it('removes the particle resize listener when the home page unmounts', () => {
    expect(homeViewSource).toContain("window.addEventListener('resize', resize)")
    expect(homeViewSource).toContain("window.removeEventListener('resize', homeResizeHandler)")
  })
})
