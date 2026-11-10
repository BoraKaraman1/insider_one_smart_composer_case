import { errorFrom } from './client'

export interface SSEMessage {
  id: string | null
  event: string
  data: string
}

export function createSSEParser(onMessage: (m: SSEMessage) => void, onComment?: (text: string) => void) {
  let buf = ''
  let skipLF = false // Son parça CR ile bitti, baştaki LF ona ait.
  let id: string | null = null
  let event = ''
  let data: string[] = []

  function line(l: string) {
    if (l === '') {
      if (data.length > 0) onMessage({ id, event: event || 'message', data: data.join('\n') })
      id = null
      event = ''
      data = []
      return
    }
    if (l.startsWith(':')) {
      onComment?.(l.slice(1))
      return
    }
    const colon = l.indexOf(':')
    const name = colon < 0 ? l : l.slice(0, colon)
    let value = colon < 0 ? '' : l.slice(colon + 1)
    if (value.startsWith(' ')) value = value.slice(1)
    if (name === 'data') data.push(value)
    else if (name === 'event') event = value
    else if (name === 'id' && !value.includes('\0')) id = value
  }

  return {
    push(chunk: string) {
      if (skipLF && chunk.length > 0) {
        if (chunk[0] === '\n') chunk = chunk.slice(1)
        skipLF = false
      }
      buf += chunk
      for (;;) {
        const i = buf.search(/[\r\n]/)
        if (i < 0) break
        const l = buf.slice(0, i)
        if (buf[i] === '\r' && i + 1 === buf.length) {
          skipLF = true
          buf = ''
        } else {
          buf = buf.slice(buf[i] === '\r' && buf[i + 1] === '\n' ? i + 2 : i + 1)
        }
        line(l)
      }
    },
    end() {
      buf = ''
      id = null
      event = ''
      data = []
    },
  }
}

export interface StreamOptions {
  token: string
  lastEventId?: number
  signal?: AbortSignal
  fetch?: typeof fetch
  onMessage: (m: SSEMessage) => void
  onComment?: (text: string) => void
}

export async function openEventStream(url: string, opts: StreamOptions): Promise<'ended' | 'aborted'> {
  const doFetch = opts.fetch ?? ((input, init) => fetch(input, init))
  const headers: Record<string, string> = { Authorization: `Bearer ${opts.token}`, Accept: 'text/event-stream' }
  if (opts.lastEventId !== undefined) headers['Last-Event-ID'] = String(opts.lastEventId)
  const aborted = () => opts.signal?.aborted === true
  try {
    const res = await doFetch(url, { headers, signal: opts.signal, cache: 'no-store' })
    if (!res.ok) throw await errorFrom(res)
    if (!res.body) return 'ended'
    const parser = createSSEParser(
      (m) => {
        if (!aborted()) opts.onMessage(m)
      },
      (c) => {
        if (!aborted()) opts.onComment?.(c)
      },
    )
    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    for (;;) {
      const { value, done } = await reader.read()
      if (done) break
      parser.push(decoder.decode(value, { stream: true }))
      if (aborted()) {
        await reader.cancel().catch(() => {})
        return 'aborted'
      }
    }
    parser.push(decoder.decode())
    parser.end()
    return 'ended'
  } catch (e) {
    if (aborted()) return 'aborted'
    throw e
  }
}
