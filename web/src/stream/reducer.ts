// The pure reducer over generation events (plan 1.2). It never mutates its
// input. Ids at or below lastEventId are replays and change nothing.

import type { Issue } from '../api/issues'

export interface GenEvent {
  id: number
  event: string
  data: any
}

export interface GenState {
  phase: 'idle' | 'streaming' | 'done' | 'failed'
  version: number | null
  segment: unknown | null // the segment_ready payload: rule, description, counts
  fields: Record<string, string> // sms.body, email.subject, email.body; provisional
  attempt: number
  errors: unknown[]
  lastEventId: number
}

export function initialState(): GenState {
  return { phase: 'idle', version: null, segment: null, fields: {}, attempt: 0, errors: [], lastEventId: 0 }
}

// issuesOf keeps the server's issues, or names the failure when it sent
// none (a transport failure), so an error is never shown as nothing.
function issuesOf(data: any, fallback: string): unknown[] {
  if (Array.isArray(data?.errors) && data.errors.length > 0) return data.errors
  const issue: Issue = { path: '', code: String(data?.reason ?? fallback), message: [data?.stage, data?.class].filter(Boolean).join(': ') || fallback }
  return [issue]
}

export function reduce(s: GenState, e: GenEvent): GenState {
  if (e.id <= s.lastEventId) return s
  const at = { ...s, lastEventId: e.id }
  if (e.event === 'started') {
    return { ...initialState(), phase: 'streaming', version: e.data?.version ?? null, lastEventId: e.id }
  }
  if (s.phase === 'done' || s.phase === 'failed') return at
  switch (e.event) {
    case 'segment_ready':
      return { ...at, segment: e.data }
    case 'delta': {
      const field = String(e.data?.field)
      return {
        ...at,
        attempt: e.data?.attempt ?? s.attempt,
        fields: { ...s.fields, [field]: (s.fields[field] ?? '') + String(e.data?.text ?? '') },
      }
    }
    case 'attempt_failed':
      return { ...at, attempt: e.data?.attempt ?? s.attempt, fields: {}, errors: issuesOf(e.data, 'attempt_failed') }
    case 'done':
      return { ...at, phase: 'done' }
    case 'failed':
      return { ...at, phase: 'failed', errors: issuesOf(e.data, 'failed') }
    default:
      return at
  }
}
