// Plan 3, 1.7: SSE through nginx, against the compose stack on :8081 in
// mock mode (make e2e). Uses the app's own src/api/sse.ts in Node.
//
// Env: WEB_URL (default http://127.0.0.1:8081), E2E_TOKEN (default the
// compose dev token). No API key: the stack runs LLM_MODE=mock.
//
// Contract assumed besides sse.ts (src/composables/useGeneration.ts):
//   useGeneration(opts: { baseUrl: string; token: string; fetch?: typeof fetch;
//                         getCampaign(id, signal): Promise<CampaignView> }): {
//     state: Ref<GenState>; view: Ref<CampaignView | null>; endedEarly: Ref<boolean>;
//     follow(campaignId: string, eventsUrl: string): Promise<void>  resolves once terminal or reconciled
//   }
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

// An idle stream: after the last event of a finished generation, so the
// server waits for live events and sends only heartbeats.
async function idleStreamURL(): Promise<{ id: string; url: string }> {
  const id = await createCampaign()
  const first = await collect(await generate(id))
  const last = first.seen[first.seen.length - 1]
  expect(last.m.event).toBe('done')
  await untilState(id, ['in_review'])
  return { id, url: `/api/campaigns/${id}/events?after=${last.m.id}` }
}

describe('SSE through nginx', () => {
  // 1. The proxy does not buffer: a heartbeat comment, a few bytes long,
  // reaches the client while the stream is idle, and live events arrive on
  // the open stream in order.
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

  // 2. Mid-run of a second generation, the campaign view's events_url never
  // yields the first generation's terminal event.
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

  // 3. Drop the connection mid-stream and reconnect with Last-Event-ID:
  // every remaining event arrives exactly once.
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

  // 4. A heartbeat keeps an idle stream open past nginx's default 60 s read
  // timeout (the config sets 1 h; heartbeats make the margin irrelevant).
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

  // 5. A stream cut before its terminal event: the client reloads the
  // campaign view and reconciles, either by reconnecting (still generating)
  // or by rendering the stored revision with an early-end notice.
  it('reconciles from the campaign view when the stream is cut', async () => {
    const id = await createCampaign()
    const url = await generate(id)
    let connections = 0
    // The first connection is cut after its first frame, as a proxy reset would.
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
      expect(connections).toBeGreaterThanOrEqual(2) // it reconnected
      expect(g.state.value.phase).toBe('done')
      expect(g.state.value.fields['sms.body']).toBe(view.revision.sms_body)
      expect(g.state.value.fields['email.subject']).toBe(view.revision.email_subject)
      expect(g.state.value.fields['email.body']).toBe(view.revision.email_body)
    }
  }, 60_000)
})
