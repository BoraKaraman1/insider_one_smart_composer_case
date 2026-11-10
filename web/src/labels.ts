// Display names for API state values. Unknown values show as they are.
const campaignStates: Record<string, string> = {
  draft: 'Draft',
  generating: 'Generating',
  generation_failed: 'Generation failed',
  in_review: 'In review',
  approved: 'Approved',
  sending: 'Sending',
  completed: 'Completed',
  completed_with_failures: 'Completed with failures',
  rejected: 'Rejected',
  cancelled: 'Cancelled',
}

const messageStates: Record<string, string> = {
  sent: 'Sent',
  sending: 'Sending',
  queued: 'Queued',
  retrying: 'Retrying',
  failed: 'Failed',
  dead: 'Dead',
  skipped: 'Skipped',
}

const channels: Record<string, string> = { sms: 'SMS', email: 'Email' }

// Where each campaign state leads: nothing left to do, still working, needs a person, or finished well.
export const campaignTone = (s: string): 'done' | 'work' | 'review' | 'bad' =>
  ['completed', 'approved'].includes(s) ? 'done'
  : ['generating', 'sending'].includes(s) ? 'work'
  : ['draft', 'in_review'].includes(s) ? 'review'
  : 'bad'
export const isFinished = (s: string) => ['completed', 'completed_with_failures', 'rejected', 'cancelled'].includes(s)

export const campaignState = (s: string) => campaignStates[s] ?? s
export const messageState = (s: string) => messageStates[s] ?? s
export const channelName = (c: string) => channels[c] ?? c
