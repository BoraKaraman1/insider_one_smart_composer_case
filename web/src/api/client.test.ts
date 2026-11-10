import { describe, expect, it, vi } from 'vitest'
import { ApiError, createApi } from './client'

describe('createApi', () => {
  it('sends the token only in the Authorization header', async () => {
    const f = vi.fn(async (_u: string, _i?: RequestInit) => new Response('{"messages":[]}', { status: 200 }))
    const api = createApi({ token: () => 'tok', fetch: f as unknown as typeof fetch })
    await expect(api.getAttention('c1')).resolves.toEqual([])
    const [url, init] = f.mock.calls[0]
    expect(url).toBe('/api/campaigns/c1/messages?filter=attention')
    expect(new Headers(init?.headers).get('Authorization')).toBe('Bearer tok')
  })

  it('throws ApiError with the server issues, and calls onUnauthorized on 401', async () => {
    const onUnauthorized = vi.fn()
    const body = '{"errors":[{"path":"","code":"unauthorized","message":"bad token"}]}'
    const api = createApi({ token: () => 'x', fetch: (async () => new Response(body, { status: 401 })) as typeof fetch, onUnauthorized })
    const err = await api.getCampaign('c1').catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(401)
    expect(err.issues[0].code).toBe('unauthorized')
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
  })

  it('never drops a failure whose body is not the error shape', async () => {
    const api = createApi({ token: () => 'x', fetch: (async () => new Response('bad gateway', { status: 502 })) as typeof fetch })
    const err: ApiError = await api.getStats('c1').catch((e) => e)
    expect(err.status).toBe(502)
    expect(err.issues).toEqual([{ path: '', code: 'http_502', message: 'bad gateway' }])
  })
})
