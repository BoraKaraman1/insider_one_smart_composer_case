import { describe, expect, it } from 'vitest'
import { mapIssues, type Issue } from './issues'

const issue = (path: string, code = 'c'): Issue => ({ path, code, message: `${code} at ${path}` })

describe('mapIssues', () => {
  it('maps the three copy fields exactly', () => {
    const m = mapIssues([
      issue('/revision/content/sms/body', 'contains_url'),
      issue('/revision/content/email/subject', 'too_long'),
      issue('/revision/content/email/body', 'unknown_variable'),
    ])
    expect(m.sms.map((i) => i.code)).toEqual(['contains_url'])
    expect(m.emailSubject.map((i) => i.code)).toEqual(['too_long'])
    expect(m.emailBody.map((i) => i.code)).toEqual(['unknown_variable'])
    expect(m.segment).toEqual([])
    expect(m.form).toEqual([])
  })

  it('sends anything under /revision/segment to the segment card', () => {
    const m = mapIssues([issue('/revision/segment'), issue('/revision/segment/conditions/0/value'), issue('/revision/segment/match')])
    expect(m.segment).toHaveLength(3)
  })

  it('sends version, base_revision_id and unmapped paths to the form list', () => {
    const paths = [
      '/version',
      '/base_revision_id',
      '',
      '/revision',
      '/revision/content',
      '/revision/content/sms',
      '/revision/content/email/bodyx',
      '/revision/segmentx',
      '/revision/content/sms/body/0',
      '/something/else',
    ]
    const m = mapIssues(paths.map((p) => issue(p)))
    expect(m.form.map((i) => i.path)).toEqual(paths)
    expect(m.sms).toEqual([])
    expect(m.segment).toEqual([])
  })

  it('never drops an issue and keeps the order within a list', () => {
    const all = [
      issue('/revision/content/sms/body', 'a'),
      issue('/x', 'b'),
      issue('/revision/content/sms/body', 'c'),
      issue('/revision/segment/conditions/1', 'd'),
      issue('/revision/content/email/body', 'e'),
    ]
    const m = mapIssues(all)
    const total = m.sms.length + m.emailSubject.length + m.emailBody.length + m.segment.length + m.form.length
    expect(total).toBe(all.length)
    expect(m.sms.map((i) => i.code)).toEqual(['a', 'c'])
  })
})
