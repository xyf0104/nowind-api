import { describe, expect, it } from 'vitest'
import router from '../index'

describe('Team mailbox share route', () => {
  it('keeps the independent mailbox page outside authentication and the admin shell', () => {
    const route = router.getRoutes().find((record) => record.name === 'TeamMailboxShare')
    expect(route?.path).toBe('/team-mail')
    expect(route?.meta.requiresAuth).toBe(false)
    expect(route?.meta.requiresAdmin).toBe(false)
  })
})
