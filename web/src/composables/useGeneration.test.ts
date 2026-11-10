// useGeneration: reconnect with Last-Event-ID, reconcile from the view, and
// never reopen a stream after its terminal event (plan 1.2).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope } from 'vue'
import type { CampaignView } from '../api/types'
import { useGeneration } from './useGeneration'

const frame = (id: number, event: string, data: unknown) => `id: ${id}\nevent: ${event}\ndata: ${JSON.stringify(data)}\n\n`

function sse(...frames: string[]): Response {
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      frames.forEach((f) => c.enqueue(new TextEncoder().encode(f)))
      c.close()
    },
  })
  return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
}

const view = (state: string) => ({ campaign: { id: 'c1', state, version: 2 }, revision: null, events: [], generation: null }) as unknown as CampaignView

function setup(responses: (() => Response | Promise<Response>)[], views: (() => CampaignView | Promise<CampaignView>)[]) {
  const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => responses.shift()!())
  const getCampaign = vi.fn(async () => views.shift()!())
  const onUnauthorized = vi.fn()
  const scope = effectScope()
  const g = scope.run(() =>
    useGeneration({ token: 't', fetch: fetchMock as unknown as typeof fetch, getCampaign, onUnauthorized }),
  )!
  return { g, scope, fetchMock, getCampaign, onUnauthorized }
}

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('useGeneration', () => {
  it('reconnects with Last-Event-ID when the stream ends while still generating', async () => {
    const s = setup(
      [
        () => sse(frame(5, 'started', { version: 2 }), frame(6, 'delta', { field: 'sms.body', text: 'Mer', attempt: 1 })),
        () => sse(frame(7, 'delta', { field: 'sms.body', text: 'haba', attempt: 1 }), frame(8, 'done', {})),
      ],
      [() => view('generating'), () => view('in_review')],
    )
    const done = s.g.follow('c1', '/api/campaigns/c1/events?after=4')
    await vi.advanceTimersByTimeAsync(1_000)
    await done
    expect(s.fetchMock).toHaveBeenCalledTimes(2)
    expect(s.fetchMock.mock.calls[1][0]).toBe('/api/campaigns/c1/events?after=4')
    expect(new Headers(s.fetchMock.mock.calls[0][1]?.headers).get('Last-Event-ID')).toBeNull()
    expect(new Headers(s.fetchMock.mock.calls[1][1]?.headers).get('Last-Event-ID')).toBe('6')
    expect(s.g.state.value.phase).toBe('done')
    expect(s.g.state.value.fields['sms.body']).toBe('Merhaba')
    expect(s.g.view.value?.campaign.state).toBe('in_review')
    expect(s.g.endedEarly.value).toBe(false)
    s.scope.stop()
  })

  it('renders from the view with a notice when the stream ended early', async () => {
    const s = setup([() => sse(frame(5, 'started', { version: 2 }))], [() => view('cancelled')])
    await s.g.follow('c1', '/e')
    expect(s.g.endedEarly.value).toBe(true)
    expect(s.g.view.value?.campaign.state).toBe('cancelled')
    expect(s.fetchMock).toHaveBeenCalledTimes(1)
    s.scope.stop()
  })

  it('backs off and reconnects after a dropped connection', async () => {
    const s = setup(
      [
        () => Promise.reject(new TypeError('connection reset')),
        () => Promise.reject(new TypeError('connection reset')),
        () => sse(frame(5, 'started', { version: 2 }), frame(6, 'done', {})),
      ],
      [() => view('in_review')],
    )
    const done = s.g.follow('c1', '/e')
    await vi.advanceTimersByTimeAsync(499)
    expect(s.fetchMock).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(s.fetchMock).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1_000) // doubled
    await done
    expect(s.fetchMock).toHaveBeenCalledTimes(3)
    expect(s.g.state.value.phase).toBe('done')
    s.scope.stop()
  })

  it('never reopens the stream after done, even when the view reload fails', async () => {
    const s = setup(
      [() => sse(frame(5, 'started', { version: 2 }), frame(6, 'done', {}))],
      [() => Promise.reject(new TypeError('down')), () => view('in_review')],
    )
    const done = s.g.follow('c1', '/e')
    await vi.advanceTimersByTimeAsync(1_000)
    await done
    expect(s.fetchMock).toHaveBeenCalledTimes(1)
    expect(s.getCampaign).toHaveBeenCalledTimes(2)
    expect(s.g.view.value?.campaign.state).toBe('in_review')
    expect(s.g.error.value).toBeNull()
    s.scope.stop()
  })

  it('stops on 401 and hands back to the token gate', async () => {
    const s = setup([() => new Response('{"errors":[{"path":"","code":"unauthorized","message":"no"}]}', { status: 401 })], [])
    await s.g.follow('c1', '/e')
    expect(s.onUnauthorized).toHaveBeenCalledTimes(1)
    expect(s.g.error.value).toBe('no')
    expect(s.fetchMock).toHaveBeenCalledTimes(1)
    s.scope.stop()
  })

  it('aborts the stream when the scope is disposed', async () => {
    let signal: AbortSignal | undefined
    const fetchMock = vi.fn(
      (_u: string, init?: RequestInit) =>
        new Promise<Response>((_, reject) => {
          signal = init?.signal ?? undefined
          signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
        }),
    )
    const scope = effectScope()
    const g = scope.run(() => useGeneration({ token: 't', fetch: fetchMock as unknown as typeof fetch, getCampaign: vi.fn() }))!
    const done = g.follow('c1', '/e')
    await vi.advanceTimersByTimeAsync(0)
    scope.stop()
    await done
    expect(signal?.aborted).toBe(true)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})
