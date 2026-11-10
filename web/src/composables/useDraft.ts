// The edit contract (plan 1.3). useDraft owns the unsaved text. Campaign
// reloads go to observe() and never write into the draft; only useLatest()
// discards it. A 409 keeps it, a 422 keeps it and maps the issues.

import { computed, ref } from 'vue'
import { ApiError } from '../api/client'
import { mapIssues, type IssueMap } from '../api/issues'
import type { CampaignView, RevisionRequest } from '../api/types'

export interface Draft {
  sms: string
  subject: string
  body: string
}

interface Base {
  revisionId: string
  version: number
  segment: unknown
  copy: Draft
}

function baseOf(v: CampaignView): Base | null {
  const r = v.revision
  if (!r) return null
  return {
    revisionId: r.id,
    version: v.campaign.version,
    segment: r.segment,
    copy: { sms: r.sms_body, subject: r.email_subject, body: r.email_body },
  }
}

export function useDraft() {
  const draft = ref<Draft | null>(null)
  const base = ref<Base | null>(null)
  const conflict = ref(false)
  const issues = ref<IssueMap | null>(null)
  const latest = ref<CampaignView | null>(null) // the newest view seen, for Compare

  const dirty = computed(() => {
    const d = draft.value
    const b = base.value?.copy
    return !!d && !!b && (d.sms !== b.sms || d.subject !== b.subject || d.body !== b.body)
  })

  function reset(v: CampaignView) {
    const b = baseOf(v)
    if (!b) return
    base.value = b
    draft.value = { ...b.copy }
    conflict.value = false
    issues.value = null
  }

  return {
    draft,
    dirty,
    conflict,
    issues,
    latest,

    // start fills the draft from the current revision, only when it is empty.
    start(v: CampaignView) {
      latest.value = v
      if (draft.value === null) reset(v)
    },

    // observe records a reload. It never touches the draft.
    observe(v: CampaignView) {
      latest.value = v
    },

    body(): RevisionRequest {
      const b = base.value
      const d = draft.value
      if (!b || !d) throw new Error('useDraft: no draft to send')
      return {
        base_revision_id: b.revisionId,
        version: b.version,
        revision: {
          segment: b.segment,
          content: { sms: { body: d.sms }, email: { subject: d.subject, body: d.body } },
        },
      }
    },

    async save(post: (body: RevisionRequest) => Promise<CampaignView>): Promise<'saved' | 'conflict' | 'invalid'> {
      try {
        const v = await post(this.body())
        latest.value = v
        // The text stays as typed; the saved revision becomes the base.
        base.value = baseOf(v)
        conflict.value = false
        issues.value = null
        return 'saved'
      } catch (e) {
        if (e instanceof ApiError && e.status === 409) {
          conflict.value = true
          return 'conflict'
        }
        if (e instanceof ApiError && e.status === 422) {
          issues.value = mapIssues(e.issues)
          return 'invalid'
        }
        throw e
      }
    },

    // rebase is "Save on top of latest": the latest revision becomes the
    // base, the text is kept.
    rebase(v: CampaignView) {
      latest.value = v
      const b = baseOf(v)
      if (b) base.value = b
      conflict.value = false
    },

    // useLatest is "Use latest": discard the draft for the latest revision.
    useLatest(v: CampaignView) {
      latest.value = v
      reset(v)
    },
  }
}
