import { computed, ref, watch, type Ref } from 'vue'
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
  const latest = ref<CampaignView | null>(null)

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

  const approvable = computed(() => !!base.value && !dirty.value && !conflict.value)

  function start(v: CampaignView) {
    latest.value = v
    if (draft.value === null) reset(v)
  }

  // Temiz taslak yeni sürüme geçer, düzenlenmiş taslak korunur ve çakışma olarak işaretlenir.
  function observe(v: CampaignView) {
    latest.value = v
    const b = base.value
    if (!v.revision || !b || v.revision.id === b.revisionId) return
    if (dirty.value || conflict.value) conflict.value = true
    else reset(v)
  }

  return {
    draft,
    dirty,
    approvable,
    conflict,
    issues,
    latest,
    start,
    observe,

    track(view: Ref<CampaignView | null>) {
      watch(view, (v) => {
        if (!v) return
        if (draft.value === null) start(v)
        else observe(v)
      })
    },

    // Onay ekrandaki sürümle gider, görünmeyen ya da çakışan sürüm onaylanamaz.
    approval(): string | null {
      return approvable.value ? base.value!.revisionId : null
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
        // Yazılan metin durur, kayıt yeni taban olur.
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

    rebase(v: CampaignView) {
      latest.value = v
      const b = baseOf(v)
      if (b) base.value = b
      conflict.value = false
    },

    useLatest(v: CampaignView) {
      latest.value = v
      reset(v)
    },
  }
}
