import { describe, expect, it, vi } from 'vitest'
import { createSSEParser, openEventStream, type SSEMessage } from './sse'

function parseAll(chunks: string[]): SSEMessage[] {
  const out: SSEMessage[] = []
  const p = createSSEParser((m) => out.push(m))
  chunks.forEach((c) => p.push(c))
  p.end()
  return out
}

const frame = 'id: 7\nevent: delta\ndata: {"field":"sms.body","text":"Mer"}\n\n'

describe('SSE parser', () => {
  it('parses one frame', () => {
    expect(parseAll([frame])).toEqual([{ id: '7', event: 'delta', data: '{"field":"sms.body","text":"Mer"}' }])
  })

  it('handles chunks split anywhere, including inside a field name and the blank line', () => {
    const whole = frame + 'id: 8\nevent: done\ndata: {}\n\n'
    const want = parseAll([whole])
    for (let i = 1; i < whole.length; i++) {
      expect(parseAll([whole.slice(0, i), whole.slice(i)])).toEqual(want)
    }
    expect(parseAll(whole.split(''))).toEqual(want)
  })

  it('skips heartbeat comments', () => {
    const out = parseAll([': heartbeat\n\n', frame, ':\n\n', ': ping\n'])
    expect(out).toHaveLength(1)
    expect(out[0].event).toBe('delta')
  })

  it('joins multi-line data with newlines', () => {
    expect(parseAll(['event: x\ndata: a\ndata: b\ndata:\ndata: c\n\n'])[0].data).toBe('a\nb\n\nc')
  })

  it('accepts CRLF and CR line endings', () => {
    expect(parseAll(['id: 1\r\nevent: x\r\ndata: y\r\n\r\n'])).toEqual([{ id: '1', event: 'x', data: 'y' }])
    expect(parseAll(['id: 1\revent: x\rdata: y\r\r'])).toEqual([{ id: '1', event: 'x', data: 'y' }])
  })

  it('strips one leading space only, and defaults the event to message', () => {
    expect(parseAll(['data:  two spaces\n\n'])[0]).toEqual({ id: null, event: 'message', data: ' two spaces' })
  })

  it('drops an event cut off before its blank line', () => {
    expect(parseAll([frame, 'id: 9\nevent: delta\ndata: {"partial"'])).toHaveLength(1)
  })

  it('ignores a frame with no data', () => {
    expect(parseAll(['event: x\n\n'])).toEqual([])
  })
})

function streamResponse(chunks: string[], status = 200): Response {
  const enc = new TextEncoder()
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      chunks.forEach((s) => c.enqueue(enc.encode(s)))
      c.close()
    },
  })
  return new Response(body, { status, headers: { 'Content-Type': 'text/event-stream' } })
}

describe('openEventStream', () => {
  it('sends the token only in the Authorization header, and Last-Event-ID when resuming', async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => streamResponse([frame]))
    const got: SSEMessage[] = []
    const result = await openEventStream('/api/campaigns/c/events?after=4', {
      token: 'tok',
      lastEventId: 6,
      fetch: fetchMock as unknown as typeof fetch,
      onMessage: (m) => got.push(m),
    })
    expect(result).toBe('ended')
    expect(got).toHaveLength(1)
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/campaigns/c/events?after=4')
    expect(url).not.toContain('tok')
    const h = new Headers(init?.headers)
    expect(h.get('Authorization')).toBe('Bearer tok')
    expect(h.get('Last-Event-ID')).toBe('6')
    expect(h.get('Accept')).toBe('text/event-stream')
  })

  it('reports aborted when the signal fires', async () => {
    const ctl = new AbortController()
    const fetchMock = vi.fn(async (_u: string, init?: RequestInit) => {
      return new Promise<Response>((_, reject) => init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError'))))
    })
    const p = openEventStream('/x', { token: 't', signal: ctl.signal, fetch: fetchMock as unknown as typeof fetch, onMessage: () => {} })
    ctl.abort()
    await expect(p).resolves.toBe('aborted')
  })

  it('rejects with the status on a non-2xx answer', async () => {
    const fetchMock = vi.fn(async () => new Response('{"errors":[]}', { status: 401 }))
    await expect(openEventStream('/x', { token: 't', fetch: fetchMock as unknown as typeof fetch, onMessage: () => {} })).rejects.toMatchObject({
      status: 401,
    })
  })
})
