// The campaign view of one page, and the actions on it. A 409 on any action
// reloads the view and says what changed; it never touches a draft.

import { ref, type Ref } from 'vue'
import { ApiError } from '../api/client'
import type { Api } from '../api/client'
import type { CampaignView } from '../api/types'

export function useCampaign(id: Ref<string>, api: Api) {
  const view = ref<CampaignView | null>(null)
  const error = ref<string | null>(null)
  const notice = ref<string | null>(null)
  const busy = ref(false)

  async function reload(): Promise<CampaignView | null> {
    try {
      view.value = await api.getCampaign(id.value)
      error.value = null
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    }
    return view.value
  }

  // act runs one action. On success the answer is the new view (or the
  // caller reloads); on 409 the view is reloaded and a notice says why.
  async function act<T>(name: string, fn: () => Promise<T>): Promise<T | null> {
    busy.value = true
    notice.value = null
    error.value = null
    const before = view.value?.campaign
    try {
      return await fn()
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        const now = (await reload())?.campaign
        const what = before && now ? ` It was ${before.state} at version ${before.version}; it is now ${now.state} at version ${now.version}.` : ''
        notice.value = `${name} did not apply: ${e.message}.${what} Your unsaved edits are kept.`
      } else {
        error.value = `${name} failed: ${e instanceof Error ? e.message : String(e)}`
      }
      return null
    } finally {
      busy.value = false
    }
  }

  return { view, error, notice, busy, reload, act }
}
