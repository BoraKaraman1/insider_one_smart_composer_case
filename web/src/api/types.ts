// Mirrors the Go JSON of internal/httpapi and internal/store. Keep the field
// names exactly as the json tags spell them.

import type { Issue } from './issues'

export type CampaignState =
  | 'draft'
  | 'generating'
  | 'in_review'
  | 'generation_failed'
  | 'approved'
  | 'sending'
  | 'completed'
  | 'completed_with_failures'
  | 'rejected'
  | 'cancelled'

// store.Campaign
export interface Campaign {
  id: string
  brief: string
  state: CampaignState
  version: number
  campaign_url: string | null
  current_revision_id: string | null
  approved_revision_id: string | null
  approved_by: string | null
  approved_at: string | null
  created_at: string
  updated_at: string
}

// A segment rule (docs/segment-rules.md). The UI shows it and sends it back
// unchanged; the server validates it.
export interface SegmentRule {
  version: number
  match: 'all' | 'any'
  conditions: unknown[]
}

// store.Revision
export interface Revision {
  id: string
  campaign_id: string
  n: number
  source: string
  segment: SegmentRule
  sms_body: string
  email_subject: string
  email_body: string
  warnings: Issue[] | null
  audience_total: number | null
  sms_eligible: number | null
  email_eligible: number | null
  created_at: string
}

// store.Event: one audit trail entry.
export interface AuditEvent {
  from_state: string | null
  to_state: string
  actor: string
  reason: string
  at: string
}

// httpapi.campaignView
export interface CampaignView {
  campaign: Campaign
  revision: Revision | null
  events: AuditEvent[]
  generation: { events_url: string } | null
}

// httpapi.generateResponse, the 202 of POST /generate.
export interface GenerateResponse {
  version: number
  events_url: string
}

// The body of POST /revisions (plan 1.3).
export interface RevisionRequest {
  base_revision_id: string
  version: number
  revision: {
    segment: unknown
    content: {
      sms: { body: string }
      email: { subject: string; body: string }
    }
  }
}

// segment.Counts
export interface SegmentCounts {
  total: number
  sms_eligible: number
  email_eligible: number
}

// The payload of a segment_ready event.
export interface SegmentReady {
  rule: SegmentRule
  description: string
  counts: SegmentCounts
}

// httpapi.statsResponse: channel -> message state -> count. States are
// store.MessageStates: queued, retrying, sending, sent, failed, dead, skipped.
export interface Stats {
  channels: Record<string, Record<string, number>>
}

// store.AttentionItem. It never carries contact data.
export interface AttentionItem {
  id: string
  channel: string
  state: string
  attempts: number
  reason: string // stuck_sending, not_started, overdue, many_attempts, failed, dead
  last_error_class: string | null
  last_error: string | null
  next_attempt_at: string | null
  updated_at: string
}
