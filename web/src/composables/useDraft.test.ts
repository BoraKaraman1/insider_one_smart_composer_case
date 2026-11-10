import { describe, expect, it, vi } from 'vitest'
import { effectScope } from 'vue'
import { ApiError } from '../api/client'
import { useDraft } from './useDraft'

const segment = { version: 1, match: 'all', conditions: [{ kind: 'enum_attribute', field: 'city', op: 'eq', value: 'istanbul' }] }

function view(revisionId: string, version: number, copy = { sms: 'Merhaba {{campaign_url}}', subject: 'Sepet', body: 'Selam {{campaign_url}}' }, seg: unknown = segment) {
  return {
    campaign: { id: 'c1', state: 'in_review', version, current_revision_id: revisionId },
    revision: { id: revisionId, n: 1, source: 'llm', segment: seg, sms_body: copy.sms, email_subject: copy.subject, email_body: copy.body },
    events: [],
    generation: null,
  } as any
}

function withDraft() {
  const scope = effectScope()
  const d = scope.run(() => useDraft())!
  return { d, scope }
}

describe('useDraft', () => {
  it('starts from the current revision and tracks edits', () => {
    const { d } = withDraft()
    d.start(view('r1', 3))
    expect(d.draft.value).toEqual({ sms: 'Merhaba {{campaign_url}}', subject: 'Sepet', body: 'Selam {{campaign_url}}' })
    expect(d.dirty.value).toBe(false)
    d.draft.value!.sms = 'Merhaba {{first_name}} {{campaign_url}}'
    expect(d.dirty.value).toBe(true)
  })

  it('builds the exact request body, with the segment copied from the base revision', () => {
    const { d } = withDraft()
    d.start(view('r1', 3))
    d.draft.value!.subject = 'Yeni konu'
    expect(d.body()).toEqual({
      base_revision_id: 'r1',
      version: 3,
      revision: {
        segment,
        content: { sms: { body: 'Merhaba {{campaign_url}}' }, email: { subject: 'Yeni konu', body: 'Selam {{campaign_url}}' } },
      },
    })
  })

  it('never lets a reload overwrite the draft', () => {
    const { d } = withDraft()
    d.start(view('r1', 3))
    d.draft.value!.sms = 'my edit {{campaign_url}}'
    d.observe(view('r2', 5, { sms: 'server copy', subject: 'server', body: 'server' }))
    d.start(view('r2', 5, { sms: 'server copy', subject: 'server', body: 'server' }))
    expect(d.draft.value!.sms).toBe('my edit {{campaign_url}}')
    expect(d.body().base_revision_id).toBe('r1')
  })

  it('keeps the draft on a 409 and reports the conflict', async () => {
    const { d } = withDraft()
    d.start(view('r1', 3))
    d.draft.value!.sms = 'my edit {{campaign_url}}'
    const post = vi.fn(async () => {
      throw new ApiError(409, [{ path: '', code: 'version_conflict', message: 'the campaign changed' }])
    })
    await expect(d.save(post)).resolves.toBe('conflict')
    expect(d.conflict.value).toBe(true)
    expect(d.draft.value!.sms).toBe('my edit {{campaign_url}}')
    expect(d.dirty.value).toBe(true)
  })

  it('keeps the draft on a 422 and maps the issues to fields', async () => {
    const { d } = withDraft()
    d.start(view('r1', 3))
    d.draft.value!.sms = 'go to https://evil.example'
    const post = vi.fn(async () => {
      throw new ApiError(422, [
        { path: '/revision/content/sms/body', code: 'contains_url', message: 'no links' },
        { path: '/version', code: 'invalid_type', message: 'x' },
      ])
    })
    await expect(d.save(post)).resolves.toBe('invalid')
    expect(d.issues.value!.sms.map((i) => i.code)).toEqual(['contains_url'])
    expect(d.issues.value!.form.map((i) => i.path)).toEqual(['/version'])
    expect(d.draft.value!.sms).toBe('go to https://evil.example')
  })

  it('saves on top of latest with the new base and version, keeping the text', async () => {
    const { d } = withDraft()
    d.start(view('r1', 3))
    d.draft.value!.body = 'my body {{campaign_url}}'
    await d.save(async () => {
      throw new ApiError(409, [])
    })
    const latestSegment = { ...segment, match: 'any' }
    d.rebase(view('r2', 6, { sms: 'theirs', subject: 'theirs', body: 'theirs' }, latestSegment))
    expect(d.conflict.value).toBe(false)
    const b = d.body()
    expect(b.base_revision_id).toBe('r2')
    expect(b.version).toBe(6)
    expect(b.revision.segment).toEqual(latestSegment)
    expect(b.revision.content.email.body).toBe('my body {{campaign_url}}')
  })

  it('discards the draft only through useLatest', () => {
    const { d } = withDraft()
    d.start(view('r1', 3))
    d.draft.value!.sms = 'mine'
    d.useLatest(view('r2', 6, { sms: 'theirs', subject: 's', body: 'b' }))
    expect(d.draft.value).toEqual({ sms: 'theirs', subject: 's', body: 'b' })
    expect(d.dirty.value).toBe(false)
    expect(d.body().base_revision_id).toBe('r2')
  })

  it('re-bases on the saved revision after a successful save', async () => {
    const { d } = withDraft()
    d.start(view('r1', 3))
    d.draft.value!.sms = 'new {{campaign_url}}'
    const post = vi.fn(async () => view('r3', 4, { sms: 'new {{campaign_url}}', subject: 'Sepet', body: 'Selam {{campaign_url}}' }))
    await expect(d.save(post)).resolves.toBe('saved')
    expect(post).toHaveBeenCalledTimes(1)
    expect(d.dirty.value).toBe(false)
    expect(d.issues.value).toBeNull()
    expect(d.body().base_revision_id).toBe('r3')
    expect(d.body().version).toBe(4)
  })
})
