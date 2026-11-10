export interface Issue {
  path: string
  code: string
  message: string
}

export interface IssueMap {
  sms: Issue[]
  emailSubject: Issue[]
  emailBody: Issue[]
  segment: Issue[]
  form: Issue[]
}

const fields: Record<string, keyof IssueMap> = {
  '/revision/content/sms/body': 'sms',
  '/revision/content/email/subject': 'emailSubject',
  '/revision/content/email/body': 'emailBody',
}

export function mapIssues(issues: Issue[]): IssueMap {
  const m: IssueMap = { sms: [], emailSubject: [], emailBody: [], segment: [], form: [] }
  for (const i of issues) {
    const field = Object.hasOwn(fields, i.path) ? fields[i.path] : undefined
    if (field) m[field].push(i)
    else if (i.path === '/revision/segment' || i.path.startsWith('/revision/segment/')) m.segment.push(i)
    else m.form.push(i)
  }
  return m
}
