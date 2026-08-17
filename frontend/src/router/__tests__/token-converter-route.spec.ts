import { describe, expect, it } from 'vitest'
import router from '../index'

describe('token converter routes', () => {
  it('exposes the canonical converter route without authentication', () => {
    const route = router.getRoutes().find((record) => record.name === 'TokenConverter')
    expect(route?.path).toBe('/token-converter')
    expect(route?.meta.requiresAuth).toBe(false)
    expect(route?.meta.requiresAdmin).toBe(false)
  })

  it('keeps the old admin URL as a compatibility redirect', () => {
    const route = router.getRoutes().find((record) => record.path === '/admin/token-converter')
    expect(route?.redirect).toBe('/token-converter')
  })
})
