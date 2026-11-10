// Follows one generation's event stream (plan 1.2). The events URL always
// comes from the 202 of generate or from the campaign view; this file never
// builds one.
//
// A dropped connection is reopened at the same URL with Last-Event-ID, with
// a backoff of 0.5 s doubling to 8 s. A stream that ends without done or
// failed is reconciled from the campaign view: still generating means
// reconnect, anything else means render the view and flag endedEarly.

import { getCurrentScope, onScopeDispose, ref } from 'vue'
import { ApiError } from '../api/client'
import { openEventStream } from '../api/sse'
import type { CampaignView } from '../api/types'
import { initialState, reduce, type GenState } from '../stream/reducer'

export interface GenerationOptions {
  baseUrl?: string
  token: string | (() => string)
  fetch?: typeof fetch
  getCampaign(id: string, signal: AbortSignal): Promise<CampaignView>
  onUnauthorized?: () => void
}

const minDelay = 500
const maxDelay = 8_000

export function useGeneration(opts: GenerationOptions) {
  const state = ref<GenState>(initialState())
  const view = ref<CampaignView | null>(null)
  const endedEarly = ref(false)
  const error = ref<string | null>(null)
  let ctl: AbortController | null = null

  const token = () => (typeof opts.token === 'function' ? opts.token() : opts.token)

  function stop() {
    ctl?.abort()
    ctl = null
  }

  function sleep(ms: number, signal: AbortSignal) {
    return new Promise<void>((resolve) => {
      const t = setTimeout(resolve, ms)
      signal.addEventListener('abort', () => {
        clearTimeout(t)
        resolve()
      })
    })
  }

  // fatal reports a 4xx: retrying cannot help.
  function fatal(e: unknown): boolean {
    if (!(e instanceof ApiError) || e.status < 400 || e.status >= 500) return false
    if (e.status === 401) opts.onUnauthorized?.()
    error.value = e.message
    return true
  }

  // loadView reloads the campaign view, retrying with backoff. It returns
  // null when aborted or on a 4xx.
  async function loadView(campaignId: string, signal: AbortSignal): Promise<CampaignView | null> {
    for (let delay = minDelay; !signal.aborted; delay = Math.min(delay * 2, maxDelay)) {
      try {
        const v = await opts.getCampaign(campaignId, signal)
        if (signal.aborted) return null
        view.value = v
        error.value = null
        return v
      } catch (e) {
        if (signal.aborted || fatal(e)) return null
        error.value = 'Could not reload the campaign; retrying.'
      }
      await sleep(delay, signal)
    }
    return null
  }

  // follow resets the reducer, aborts any earlier stream, and resolves once
  // the generation is terminal, reconciled, or the stream was aborted.
  async function follow(campaignId: string, eventsUrl: string): Promise<void> {
    stop()
    const mine = new AbortController()
    ctl = mine
    const signal = mine.signal
    state.value = initialState()
    endedEarly.value = false
    error.value = null
    let delay = minDelay

    while (!signal.aborted) {
      const seenBefore = state.value.lastEventId
      try {
        const result = await openEventStream((opts.baseUrl ?? '') + eventsUrl, {
          token: token(),
          lastEventId: state.value.lastEventId > 0 ? state.value.lastEventId : undefined,
          signal,
          fetch: opts.fetch,
          onMessage: (m) => {
            let data: unknown = null
            try {
              data = JSON.parse(m.data)
            } catch {
              // a frame that is not JSON carries nothing the reducer uses
            }
            state.value = reduce(state.value, { id: Number(m.id), event: m.event, data })
          },
        })
        if (result === 'aborted') return
        error.value = null
      } catch (e) {
        if (signal.aborted || fatal(e)) return
        error.value = 'The event stream dropped; reconnecting.'
      }
      const phase = state.value.phase
      if (phase === 'done' || phase === 'failed') {
        // The stored revision replaces the provisional copy.
        await loadView(campaignId, signal)
        return
      }
      if (error.value === null) {
        // Ended without done or failed: the view decides.
        const v = await loadView(campaignId, signal)
        if (!v) return
        if (v.campaign.state !== 'generating') {
          endedEarly.value = true
          return
        }
      }
      if (state.value.lastEventId > seenBefore) delay = minDelay
      await sleep(delay, signal)
      delay = Math.min(delay * 2, maxDelay)
    }
  }

  if (getCurrentScope()) onScopeDispose(stop)

  return { state, view, endedEarly, error, follow, stop }
}
