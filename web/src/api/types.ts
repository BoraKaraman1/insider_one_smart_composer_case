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

export interface SegmentRule {
  version: number
  match: 'all' | 'any'
  conditions: unknown[]
}

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

export interface AuditEvent {
  from_state: string | null
  to_state: string
  actor: string
  reason: string
  at: string
}

export interface CampaignView {
  campaign: Campaign
  revision: Revision | null
  events: AuditEvent[]
  generation: { events_url: string } | null
}

export interface GenerateResponse {
  version: number
  events_url: string
}

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

export interface SegmentCounts {
  total: number
  sms_eligible: number
  email_eligible: number
}

export interface SegmentReady {
  rule: SegmentRule
  description: string
  counts: SegmentCounts
}

export interface Stats {
  channels: Record<string, Record<string, number>>
}

export interface AttentionItem {
  id: string
  channel: string
  state: string
  attempts: number
  reason: string
  last_error_class: string | null
  last_error: string | null
  next_attempt_at: string | null
  updated_at: string
}
