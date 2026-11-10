import { ref, type Ref } from 'vue'
import { ApiError } from '../api/client'
import type { Api } from '../api/client'
import type { CampaignView } from '../api/types'
import { campaignState } from '../labels'

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
        const what = before && now ? ` Önceki durum: ${campaignState(before.state)}, sürüm ${before.version}. Şimdi: ${campaignState(now.state)}, sürüm ${now.version}.` : ''
        notice.value = `${name} uygulanamadı: ${e.message}.${what} Kaydedilmemiş düzenlemeleriniz korundu.`
      } else {
        error.value = `${name} başarısız: ${e instanceof Error ? e.message : String(e)}`
      }
      return null
    } finally {
      busy.value = false
    }
  }

  return { view, error, notice, busy, reload, act }
}
