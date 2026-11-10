import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref } from 'vue'
import { ApiError } from '../api/client'
import { useDeliveryPolling, type PollingApi } from './useDeliveryPolling'

type Counts = Record<string, number>
const stats = (sms: Counts, email: Counts = {}) => ({ channels: { sms, email } })
const view = (state: string) => ({ campaign: { id: 'c1', state, version: 5 }, revision: null, events: [], generation: null }) as any

interface Deferred<T> {
  promise: Promise<T>
  resolve(v: T): void
  reject(e: unknown): void
}
function deferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

function fakeApi(state: { campaign: string; stats: ReturnType<typeof stats> }) {
  const calls: { fn: string; id: string; at: number; signal: AbortSignal }[] = []
  let inFlight = 0
  let maxInFlight = 0
  const track = async <T>(fn: string, id: string, signal: AbortSignal, value: () => T, delay = 0): Promise<T> => {
    calls.push({ fn, id, at: Date.now(), signal })
    inFlight++
    maxInFlight = Math.max(maxInFlight, inFlight)
    try {
      if (delay) await new Promise((r) => setTimeout(r, delay))
      return value()
    } finally {
      inFlight--
    }
  }
  const delays = { campaign: 0, stats: 0 }
  const api: PollingApi = {
    getCampaign: (id, signal) => track('campaign', id, signal, () => view(state.campaign), delays.campaign),
    getStats: (id, signal) => track('stats', id, signal, () => state.stats, delays.stats),
    getAttention: (id, signal) => track('attention', id, signal, () => []),
  }
  return {
    api,
    calls,
    delays,
    maxInFlight: () => maxInFlight,
    ticks: () => calls.filter((c) => c.fn === 'campaign'),
  }
}

function start(api: PollingApi, id = ref('c1'), opts?: { onUnauthorized?: () => void }) {
  const scope = effectScope()
  const p = scope.run(() => useDeliveryPolling(id, api, opts))!
  return { p, scope, id }
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-01-01T00:00:00Z'))
})
afterEach(() => {
  vi.useRealTimers()
})

describe('useDeliveryPolling', () => {
  it('loads the campaign first, then stats and attention in parallel', async () => {
    const f = fakeApi({ campaign: 'sending', stats: stats({ sending: 1 }) })
    const { p, scope } = start(f.api)
    await vi.advanceTimersByTimeAsync(0)
    expect(f.calls[0].fn).toBe('campaign')
    expect(f.calls.slice(1, 3).map((c) => c.fn).sort()).toEqual(['attention', 'stats'])
    expect(p.view.value?.campaign.state).toBe('sending')
    expect(p.stats.value).toEqual(stats({ sending: 1 }))
    expect(p.lastUpdated.value).toBe(Date.now())
    scope.stop()
  })

  it('never overlaps ticks: the next one starts 1 s after the previous finished', async () => {
    const f = fakeApi({ campaign: 'sending', stats: stats({ sending: 1 }) })
    f.delays.campaign = 3000 // Aralıktan uzun sürsün diye.
    const { scope } = start(f.api)
    await vi.advanceTimersByTimeAsync(20_000)
    scope.stop()
    expect(f.maxInFlight()).toBeLessThanOrEqual(2)
    const t = f.ticks().map((c) => c.at)
    expect(t.length).toBeGreaterThan(2)
    for (let i = 1; i < t.length; i++) expect(t[i] - t[i - 1]).toBe(3000 + 1000)
  })

  it('keeps polling a cancelled campaign while a message is still sending, then runs one final tick', async () => {
    const state = { campaign: 'cancelled', stats: stats({ sending: 1, skipped: 10 }) }
    const f = fakeApi(state)
    const { p, scope } = start(f.api)
    await vi.advanceTimersByTimeAsync(5_500)
    expect(f.ticks().length).toBeGreaterThanOrEqual(5)
    expect(p.polling.value).toBe(true)

    state.stats = stats({ sent: 1, skipped: 10 })
    const before = f.ticks().length
    await vi.advanceTimersByTimeAsync(10_000)
    expect(f.ticks().length - before).toBe(2)
    expect(p.polling.value).toBe(false)
    expect(p.stats.value).toEqual(stats({ sent: 1, skipped: 10 }))
    scope.stop()
  })

  it('treats queued and retrying on either channel as in flight', async () => {
    for (const s of [stats({}, { queued: 1 }), stats({ retrying: 2 }), stats({}, { sending: 1 })]) {
      const f = fakeApi({ campaign: 'completed_with_failures', stats: s })
      const { p, scope } = start(f.api)
      await vi.advanceTimersByTimeAsync(3_500)
      expect(f.ticks().length).toBeGreaterThan(2)
      expect(p.polling.value).toBe(true)
      scope.stop()
    }
  })

  it('stops after the final tick for a finished campaign', async () => {
    const f = fakeApi({ campaign: 'completed', stats: stats({ sent: 3 }, { sent: 3 }) })
    const { p, scope } = start(f.api)
    await vi.advanceTimersByTimeAsync(30_000)
    expect(f.ticks().length).toBe(2)
    expect(p.polling.value).toBe(false)
    scope.stop()
  })

  it('backs off 1, 2, 4, 8, 15 s on failure, marks stale, and resets on success', async () => {
    let fail = true
    const f = fakeApi({ campaign: 'sending', stats: stats({ sending: 1 }) })
    const realGet = f.api.getCampaign
    f.api.getCampaign = async (id, signal) => {
      const r = await realGet(id, signal)
      if (fail) throw new TypeError('network down')
      return r
    }
    const { p, scope } = start(f.api)
    await vi.advanceTimersByTimeAsync(0)
    expect(p.stale.value).toBe(true)
    await vi.advanceTimersByTimeAsync(1_000 + 2_000 + 4_000 + 8_000 + 15_000 + 15_000)
    const t = f.ticks().map((c) => c.at - f.ticks()[0].at)
    expect(t).toEqual([0, 1_000, 3_000, 7_000, 15_000, 30_000, 45_000])

    fail = false
    await vi.advanceTimersByTimeAsync(15_000)
    expect(p.stale.value).toBe(false)
    const n = f.ticks().length
    await vi.advanceTimersByTimeAsync(1_000)
    expect(f.ticks().length).toBe(n + 1)
    scope.stop()
  })

  it('stops on 401 and hands back to the token gate', async () => {
    const onUnauthorized = vi.fn()
    const f = fakeApi({ campaign: 'sending', stats: stats({ sending: 1 }) })
    f.api.getCampaign = async () => {
      throw new ApiError(401, [])
    }
    const { p, scope } = start(f.api, ref('c1'), { onUnauthorized })
    await vi.advanceTimersByTimeAsync(60_000)
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
    expect(p.polling.value).toBe(false)
    expect(f.calls.filter((c) => c.fn === 'stats')).toHaveLength(0)
    scope.stop()
  })

  it('aborts the tick on unmount and discards its late response', async () => {
    const pending = deferred<any>()
    const signals: AbortSignal[] = []
    const api: PollingApi = {
      getCampaign: (_id, signal) => {
        signals.push(signal)
        return pending.promise
      },
      getStats: async () => stats({ sending: 1 }),
      getAttention: async () => [],
    }
    const { p, scope } = start(api)
    await vi.advanceTimersByTimeAsync(0)
    scope.stop()
    expect(signals[0].aborted).toBe(true)
    pending.resolve(view('sending'))
    await vi.advanceTimersByTimeAsync(10_000)
    expect(p.view.value).toBeNull()
    expect(signals).toHaveLength(1)
  })

  it('aborts on campaign switch and ignores the old campaign', async () => {
    const old = deferred<any>()
    const seen: { id: string; signal: AbortSignal }[] = []
    const api: PollingApi = {
      getCampaign: (id, signal) => {
        seen.push({ id, signal })
        return id === 'c1' ? old.promise : Promise.resolve({ ...view('sending'), campaign: { id, state: 'sending', version: 1 } })
      },
      getStats: async () => stats({ sending: 1 }),
      getAttention: async () => [],
    }
    const id = ref('c1')
    const { p, scope } = start(api, id)
    await vi.advanceTimersByTimeAsync(0)
    id.value = 'c2'
    await nextTick()
    await vi.advanceTimersByTimeAsync(0)
    expect(seen[0].signal.aborted).toBe(true)
    old.resolve({ ...view('completed'), campaign: { id: 'c1', state: 'completed', version: 9 } })
    await vi.advanceTimersByTimeAsync(0)
    expect(p.view.value?.campaign.id).toBe('c2')
    scope.stop()
  })
})
