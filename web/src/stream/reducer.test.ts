// Plan 3, 1.2 and 1.7: the pure reducer over generation events.
//
// Contract assumed (src/stream/reducer.ts):
//   initialState(): GenState
//   reduce(state: GenState, e: GenEvent): GenState    pure, never mutates state
//   GenEvent { id: number; event: string; data: any }
//   GenState { phase: 'idle' | 'streaming' | 'done' | 'failed'; version: number | null;
//              segment: unknown | null; fields: Record<string, string>; attempt: number;
//              errors: unknown[]; lastEventId: number }
import { describe, expect, it } from 'vitest'
import { initialState, reduce, type GenEvent, type GenState } from './reducer'

function run(events: GenEvent[], from: GenState = initialState()): GenState {
  return events.reduce(reduce, from)
}

function deepFreeze<T>(o: T): T {
  if (o && typeof o === 'object') {
    Object.values(o).forEach(deepFreeze)
    Object.freeze(o)
  }
  return o
}

const segmentReady = (id: number): GenEvent => ({
  id,
  event: 'segment_ready',
  data: { rule: { version: 1, match: 'all', conditions: [] }, description: 'Istanbul', counts: { total: 3 } },
})
const delta = (id: number, field: string, text: string, attempt = 1): GenEvent => ({
  id,
  event: 'delta',
  data: { stage: 'content', attempt, field, text },
})

describe('reducer', () => {
  it('starts idle', () => {
    const s = initialState()
    expect(s.phase).toBe('idle')
    expect(s.lastEventId).toBe(0)
    expect(s.fields).toEqual({})
  })

  it('applies events in order', () => {
    const s = run([
      { id: 10, event: 'started', data: { version: 3 } },
      segmentReady(11),
      delta(12, 'sms.body', 'Mer'),
      delta(13, 'sms.body', 'haba'),
      delta(14, 'email.subject', 'Sepet'),
      { id: 15, event: 'done', data: { revision_id: 'r1' } },
    ])
    expect(s.phase).toBe('done')
    expect(s.version).toBe(3)
    expect(s.segment).not.toBeNull()
    expect(s.fields).toEqual({ 'sms.body': 'Merhaba', 'email.subject': 'Sepet' })
    expect(s.lastEventId).toBe(15)
  })

  it('ignores replayed ids at or below lastEventId', () => {
    const events = [{ id: 10, event: 'started', data: { version: 1 } }, delta(11, 'sms.body', 'Mer'), delta(12, 'sms.body', 'haba')]
    const once = run(events)
    const replayed = run([...events, delta(11, 'sms.body', 'Mer'), delta(12, 'sms.body', 'haba')], initialState())
    expect(replayed.fields['sms.body']).toBe('Merhaba')
    expect(run([delta(12, 'sms.body', 'haba')], once).fields['sms.body']).toBe('Merhaba')
    expect(run([delta(5, 'sms.body', 'x')], once).lastEventId).toBe(12)
  })

  it('resets everything on started', () => {
    const first = run([
      { id: 1, event: 'started', data: { version: 1 } },
      segmentReady(2),
      delta(3, 'sms.body', 'old'),
      { id: 4, event: 'attempt_failed', data: { stage: 'content', attempt: 1, errors: [{ path: '/sms/body', code: 'x', message: 'm' }] } },
    ])
    const s = reduce(first, { id: 9, event: 'started', data: { version: 2 } })
    expect(s.version).toBe(2)
    expect(s.phase).toBe('streaming')
    expect(s.segment).toBeNull()
    expect(s.fields).toEqual({})
    expect(s.errors).toEqual([])
    expect(s.lastEventId).toBe(9)
  })

  it('clears the copy and records errors on attempt_failed', () => {
    const errors = [{ path: '/sms/body', code: 'unknown_variable', message: 'no {{password}}' }]
    const s = run([
      { id: 1, event: 'started', data: { version: 1 } },
      segmentReady(2),
      delta(3, 'sms.body', 'Merhaba {{password}}'),
      { id: 4, event: 'attempt_failed', data: { stage: 'content', attempt: 1, errors } },
    ])
    expect(s.fields).toEqual({})
    expect(s.errors).toEqual(errors)
    expect(s.phase).toBe('streaming')
    expect(s.segment).not.toBeNull() // a content retry keeps the segment
    const next = run([delta(5, 'sms.body', 'Merhaba', 2)], s)
    expect(next.fields['sms.body']).toBe('Merhaba')
    expect(next.attempt).toBe(2)
  })

  it('treats done and failed as terminal', () => {
    const done = run([{ id: 1, event: 'started', data: { version: 1 } }, delta(2, 'sms.body', 'a'), { id: 3, event: 'done', data: {} }])
    expect(run([delta(4, 'sms.body', 'b')], done).fields['sms.body']).toBe('a')
    expect(run([{ id: 4, event: 'failed', data: { reason: 'x' } }], done).phase).toBe('done')

    const failed = run([{ id: 1, event: 'started', data: { version: 1 } }, { id: 2, event: 'failed', data: { reason: 'refusal', stage: 'segment' } }])
    expect(failed.phase).toBe('failed')
    expect(run([delta(3, 'sms.body', 'late')], failed).fields).toEqual({})
  })

  it('ignores unknown event types but still advances the cursor', () => {
    const s = run([{ id: 1, event: 'started', data: { version: 1 } }, { id: 2, event: 'something_new', data: {} }])
    expect(s.phase).toBe('streaming')
    expect(s.lastEventId).toBe(2)
  })

  it('is pure', () => {
    const s = deepFreeze(run([{ id: 1, event: 'started', data: { version: 1 } }, delta(2, 'sms.body', 'a')]))
    const next = reduce(s, delta(3, 'sms.body', 'b'))
    expect(next).not.toBe(s)
    expect(s.fields['sms.body']).toBe('a')
    expect(next.fields['sms.body']).toBe('ab')
  })
})
