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

// Create the campaign, then start its generation. The campaign page opens
// the stream from the view's events_url.
async function submit() {
  busy.value = true
  issues.value = []
  try {
    const v = await api.createCampaign(brief.value, campaignUrl.value)
    try {
      await api.generate(v.campaign.id, v.campaign.version)
    } catch {
      // The campaign exists as a draft; its page offers Generate again.
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
      <input id="url" v-model="campaignUrl" type="url" placeholder="https://shop.example/kampanya" />
      <p class="muted">Messages link only to this URL, through the campaign_url variable.</p>
      <IssueList :issues="issues" />
      <button type="submit" :disabled="busy || !brief.trim()">Generate</button>
    </form>
  </section>
</template>
