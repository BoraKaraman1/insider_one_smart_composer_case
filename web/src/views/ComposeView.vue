<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError } from '../api/client'
import type { Issue } from '../api/issues'
import IssueList from '../components/IssueList.vue'
import { api } from '../session'

const router = useRouter()
const brief = ref('')
const campaignUrl = ref('')
const busy = ref(false)
const issues = ref<Issue[]>([])
// Valid for the default CAMPAIGN_URL_HOSTS (shop.example). Keep them under 40 characters.
const exampleUrls = [
  'https://shop.example/summer-sale',
  'https://shop.example/cart-reminder',
  'https://shop.example/welcome-back',
  'https://shop.example/new-arrivals',
  'https://shop.example/loyalty',
]

async function submit() {
  busy.value = true
  issues.value = []
  try {
    const v = await api.createCampaign(brief.value, campaignUrl.value)
    try {
      await api.generate(v.campaign.id, v.campaign.version)
    } catch {
      // Kampanya taslak kaldı, sayfası yeniden üretmeyi sunar.
    }
    await router.push(`/campaigns/${v.campaign.id}`)
  } catch (e) {
    issues.value = e instanceof ApiError ? e.issues : [{ path: '', code: 'network', message: String(e) }]
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section>
    <h2>New campaign</h2>
    <form @submit.prevent="submit">
      <label for="brief">Brief</label>
      <textarea id="brief" v-model="brief" rows="5" placeholder="Who should get what, and why." />
      <label for="url">Campaign URL</label>
      <input id="url" v-model="campaignUrl" type="url" list="example-urls" placeholder="https://shop.example/summer-sale" />
      <datalist id="example-urls"><option v-for="u in exampleUrls" :key="u" :value="u" /></datalist>
      <p class="muted">Messages link only to this URL, through the campaign_url variable. Try an example:</p>
      <div class="chips">
        <button v-for="u in exampleUrls" :key="u" type="button" class="chip" :aria-pressed="campaignUrl === u" @click="campaignUrl = u">{{ u.replace('https://', '') }}</button>
      </div>
      <IssueList :issues="issues" />
      <button type="submit" class="primary" :disabled="busy || !brief.trim()">Generate</button>
    </form>
  </section>
</template>
