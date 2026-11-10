import { describe, expect, it } from 'vitest'
import { effectScope } from 'vue'
import { openEventStream, type SSEMessage } from '../src/api/sse'
import { useGeneration } from '../src/composables/useGeneration'

const BASE = process.env.WEB_URL ?? 'http://127.0.0.1:8081'
const TOKEN = process.env.E2E_TOKEN ?? 'dev-admin-token'
const BRIEF = "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder."
const HEARTBEAT_MS = 15_000

async function api(method: string, path: string, body?: unknown): Promise<any> {
  const res = await fetch(BASE + path, {
    method,
    headers: { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await res.text()
  if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${text}`)
  return text ? JSON.parse(text) : null
}

async function createCampaign(): Promise<string> {
  const v = await api('POST', '/api/campaigns', { brief: BRIEF, campaign_url: 'https://shop.example/kampanya' })
  return v.campaign.id
}

async function generate(id: string): Promise<string> {
  const view = await api('GET', `/api/campaigns/${id}`)
  const r = await api('POST', `/api/campaigns/${id}/generate`, { version: view.campaign.version })
  return r.events_url
}

interface Seen {
  m: SSEMessage
  at: number
}

async function collect(url: string, opts: { lastEventId?: number; signal?: AbortSignal; onMessage?: (m: SSEMessage) => void } = {}) {
  const seen: Seen[] = []
  const comments: number[] = []
  const result = await openEventStream(BASE + url, {
    token: TOKEN,
    lastEventId: opts.lastEventId,
    signal: opts.signal,
    onMessage: (m) => {
      seen.push({ m, at: Date.now() })
      opts.onMessage?.(m)
    },
    onComment: () => comments.push(Date.now()),
  })
  return { seen, comments, result }
}

const ids = (s: Seen[]) => s.map((x) => Number(x.m.id))

async function untilState(id: string, states: string[], within = 30_000) {
  const end = Date.now() + within
  for (;;) {
    const v = await api('GET', `/api/campaigns/${id}`)
    if (states.includes(v.campaign.state)) return v
    if (Date.now() > end) throw new Error(`campaign ${id} is ${v.campaign.state}, want ${states}`)
    await new Promise((r) => setTimeout(r, 100))
  }
}

// Biten akıştan sonra sunucu yalnız nabız yollar.
async function idleStreamURL(): Promise<{ id: string; url: string }> {
  const id = await createCampaign()
  const first = await collect(await generate(id))
  const last = first.seen[first.seen.length - 1]
  expect(last.m.event).toBe('done')
  await untilState(id, ['in_review'])
  return { id, url: `/api/campaigns/${id}/events?after=${last.m.id}` }
}

describe('SSE through nginx', () => {
  it('delivers heartbeats and live events without buffering', async () => {
    const { id, url } = await idleStreamURL()
    const ctl = new AbortController()
    const opened = Date.now()
    let firstComment = 0
    const events: SSEMessage[] = []
    const done = openEventStream(BASE + url, {
      token: TOKEN,
      signal: ctl.signal,
      onMessage: (m) => events.push(m),
      onComment: () => {
        if (!firstComment) firstComment = Date.now()
      },
    })
    await new Promise((r) => setTimeout(r, HEARTBEAT_MS + 3_000))
    expect(firstComment, 'no heartbeat came through the proxy').toBeGreaterThan(0)
    expect(firstComment - opened).toBeLessThanOrEqual(HEARTBEAT_MS + 3_000)
    expect(events).toEqual([])

    await generate(id)
    await expect(done).resolves.toBe('ended')
    expect(events[0].event).toBe('started')
    expect(events.at(-1)!.event).toBe('done')
    expect(events.some((e) => e.event === 'delta')).toBe(true)
  }, 60_000)

  it('the view cursor skips the first generation', async () => {
    const id = await createCampaign()
    const first = await collect(await generate(id))
    const firstDone = Number(first.seen.at(-1)!.m.id)
    await untilState(id, ['in_review'])
    await generate(id)
    const view = await api('GET', `/api/campaigns/${id}`)
    expect(view.generation?.events_url).toBeTruthy()
    const second = await collect(view.generation.events_url)
    expect(second.seen[0].m.event).toBe('started')
    expect(Math.min(...ids(second.seen))).toBeGreaterThan(firstDone)
    expect(second.seen.at(-1)!.m.event).toBe('done')
  }, 60_000)

  it('resumes with Last-Event-ID without loss or duplicates', async () => {
    const id = await createCampaign()
    const url = await generate(id)
    const ctl = new AbortController()
    const part = await collect(url, { signal: ctl.signal, onMessage: () => ctl.abort() })
    expect(part.result).toBe('aborted')
    expect(part.seen.length).toBeGreaterThanOrEqual(1)
    const lastSeen = Number(part.seen.at(-1)!.m.id)

    const rest = await collect(url, { lastEventId: lastSeen })
    const full = await collect(url)
    const joined = [...ids(part.seen), ...ids(rest.seen)]
    expect(new Set(joined).size).toBe(joined.length)
    expect(joined).toEqual(ids(full.seen))
    expect(rest.seen.at(-1)!.m.event).toBe('done')
  }, 60_000)

  it('keeps an idle stream open past the proxy default timeout', async () => {
    const { id, url } = await idleStreamURL()
    const events: SSEMessage[] = []
    let comments = 0
    const done = openEventStream(BASE + url, {
      token: TOKEN,
      onMessage: (m) => events.push(m),
      onComment: () => comments++,
    })
    let ended = false
    done.then(() => (ended = true))
    await new Promise((r) => setTimeout(r, 70_000))
    expect(ended, 'the proxy closed an idle stream').toBe(false)
    expect(comments).toBeGreaterThanOrEqual(4)
    await generate(id)
    await expect(done).resolves.toBe('ended')
    expect(events[0].event).toBe('started')
  }, 120_000)

  it('reconciles from the campaign view when the stream is cut', async () => {
    const id = await createCampaign()
    const url = await generate(id)
    let connections = 0
    // İlk bağlantı ilk kareden sonra kesilir.
    const cutting: typeof fetch = async (input, init) => {
      const res = await fetch(input, init)
      if (!String(input).includes('/events') || connections++ > 0 || !res.body) return res
      const reader = res.body.getReader()
      let sent = false
      const body = new ReadableStream<Uint8Array>({
        async pull(c) {
          if (sent) {
            await reader.cancel()
            c.close()
            return
          }
          const { value, done } = await reader.read()
          if (done || !value) return c.close()
          const text = new TextDecoder().decode(value)
          const end = text.indexOf('\n\n')
          c.enqueue(new TextEncoder().encode(end >= 0 ? text.slice(0, end + 2) : text))
          sent = end >= 0
        },
      })
      return new Response(body, { status: res.status, headers: res.headers })
    }
    const scope = effectScope()
    const g = scope.run(() =>
      useGeneration({
        baseUrl: BASE,
        token: TOKEN,
        fetch: cutting,
        getCampaign: (cid, signal) =>
          fetch(`${BASE}/api/campaigns/${cid}`, { headers: { Authorization: `Bearer ${TOKEN}` }, signal }).then((r) => r.json()),
      }),
    )!
    await g.follow(id, url)
    scope.stop()

    const view = await untilState(id, ['in_review'])
    expect(connections).toBeGreaterThanOrEqual(1)
    if (g.endedEarly.value) {
      expect(g.view.value?.revision?.id).toBe(view.revision.id)
    } else {
      expect(connections).toBeGreaterThanOrEqual(2)
      expect(g.state.value.phase).toBe('done')
      expect(g.state.value.fields['sms.body']).toBe(view.revision.sms_body)
      expect(g.state.value.fields['email.subject']).toBe(view.revision.email_subject)
      expect(g.state.value.fields['email.body']).toBe(view.revision.email_body)
    }
  }, 60_000)
})
