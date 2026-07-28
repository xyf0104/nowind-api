import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post, put, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  del: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { get, post, put, delete: del }
}))

import {
  createCompositeRoute,
  deleteCompositeRoute,
  getLiveCapability,
  listCompositeRoutes,
  previewCompositeRoute,
  updateCompositeRoute
} from '@/api/admin/groups'

const route = {
  id: 9,
  group_id: 7,
  public_model: 'gpt-*',
  match_type: 'prefix' as const,
  target_platform: 'openai' as const,
  upstream_model: 'gpt-5.6',
  endpoint: 'responses' as const,
  priority: 10,
  enabled: true,
  notes: ''
}

describe('admin group v0.1.166 API contracts', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
    put.mockReset()
    del.mockReset()
  })

  it('loads the server Live capability', async () => {
    get.mockResolvedValueOnce({ data: { supported: true } })

    await expect(getLiveCapability()).resolves.toEqual({ supported: true })
    expect(get).toHaveBeenCalledWith('/admin/groups/live-capability')
  })

  it('uses the complete composite route CRUD contract', async () => {
    const input = {
      public_model: route.public_model,
      match_type: route.match_type,
      target_platform: route.target_platform,
      upstream_model: route.upstream_model,
      endpoint: route.endpoint,
      priority: route.priority,
      enabled: route.enabled,
      notes: route.notes
    }

    get.mockResolvedValueOnce({ data: [route] })
    post.mockResolvedValueOnce({ data: route })
    put.mockResolvedValueOnce({ data: route })
    del.mockResolvedValueOnce({ data: { message: 'ok' } })

    await expect(listCompositeRoutes(7)).resolves.toEqual([route])
    await expect(createCompositeRoute(7, input)).resolves.toEqual(route)
    await expect(updateCompositeRoute(7, 9, input)).resolves.toEqual(route)
    await expect(deleteCompositeRoute(7, 9)).resolves.toEqual({ message: 'ok' })

    expect(get).toHaveBeenCalledWith('/admin/groups/7/composite-routes')
    expect(post).toHaveBeenCalledWith('/admin/groups/7/composite-routes', input)
    expect(put).toHaveBeenCalledWith('/admin/groups/7/composite-routes/9', input)
    expect(del).toHaveBeenCalledWith('/admin/groups/7/composite-routes/9')
  })

  it('previews a composite route without mutating it', async () => {
    const request = { model: 'gpt-5.6', endpoint: 'responses' as const }
    const decision = {
      matched: true,
      source: 'route',
      group_id: 7,
      public_model: 'gpt-*',
      target_platform: 'openai' as const,
      upstream_model: 'gpt-5.6',
      endpoint: 'responses' as const,
      route
    }
    post.mockResolvedValueOnce({ data: decision })

    await expect(previewCompositeRoute(7, request)).resolves.toEqual(decision)
    expect(post).toHaveBeenCalledWith('/admin/groups/7/composite-routes/preview', request)
  })
})
