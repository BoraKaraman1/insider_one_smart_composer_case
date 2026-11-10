import { onScopeDispose, ref, watch, type Ref } from 'vue'
import { ApiError } from '../api/client'
import type { AttentionItem, CampaignView, Stats } from '../api/types'

export interface PollingApi {
  getCampaign(id: string, signal: AbortSignal): Promise<CampaignView>
  getStats(id: string, signal: AbortSignal): Promise<Stats>
  getAttention(id: string, signal: AbortSignal): Promise<AttentionItem[]>
}

const interval = 1_000
const backoff = [1_000, 2_000, 4_000, 8_000, 15_000]
const terminal = new Set(['completed', 'completed_with_failures', 'cancelled'])
const inFlight = ['queued', 'retrying', 'sending']

export function settled(view: CampaignView, stats: Stats): boolean {
  if (!terminal.has(view.campaign.state)) return false
  return Object.values(stats.channels).every((c) => inFlight.every((s) => (c[s] ?? 0) === 0))
}

export function useDeliveryPolling(id: Ref<string>, api: PollingApi, opts: { onUnauthorized?: () => void } = {}) {
  const view = ref<CampaignView | null>(null)
  const stats = ref<Stats | null>(null)
  const attention = ref<AttentionItem[]>([])
  const lastUpdated = ref<number | null>(null)
  const stale = ref(false)
  const polling = ref(false)

  let ctl: AbortController | null = null
  let timer: ReturnType<typeof setTimeout> | undefined
  let failures = 0
  let sawSettled = false

  function halt() {
    ctl?.abort()
    ctl = null
    clearTimeout(timer)
    timer = undefined
  }

  function next(delay: number) {
    timer = setTimeout(tick, delay)
  }

  async function tick() {
    const mine = new AbortController()
    ctl = mine
    const cid = id.value
    try {
      const v = await api.getCampaign(cid, mine.signal)
      if (mine.signal.aborted) return
      const [s, a] = await Promise.all([api.getStats(cid, mine.signal), api.getAttention(cid, mine.signal)])
      if (mine.signal.aborted) return
      view.value = v
      stats.value = s
      attention.value = a
      lastUpdated.value = Date.now()
      stale.value = false
      failures = 0
      if (settled(v, s)) {
        if (sawSettled) {
          polling.value = false // İkinci sakin tikte durur.
          return
        }
        sawSettled = true
      } else {
        sawSettled = false
      }
      next(interval)
    } catch (e) {
      if (mine.signal.aborted) return
      if (e instanceof ApiError && e.status === 401) {
        polling.value = false
        opts.onUnauthorized?.()
        return
      }
      stale.value = true
      next(backoff[Math.min(failures, backoff.length - 1)])
      failures++
    }
  }

  function begin() {
    halt()
    failures = 0
    sawSettled = false
    polling.value = true
    void tick()
  }

  watch(id, () => {
    view.value = null
    stats.value = null
    attention.value = []
    lastUpdated.value = null
    stale.value = false
    begin()
  })
  onScopeDispose(() => {
    halt()
    polling.value = false
  })
  begin()

  return { view, stats, attention, lastUpdated, stale, polling }
}
