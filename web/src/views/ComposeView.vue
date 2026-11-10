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
    <h2>Yeni kampanya</h2>
    <form @submit.prevent="submit">
      <label for="brief">Brif</label>
      <textarea id="brief" v-model="brief" rows="5" placeholder="Kim, ne almalı ve neden?" />
      <label for="url">Kampanya bağlantısı</label>
      <input id="url" v-model="campaignUrl" type="url" placeholder="https://shop.example/kampanya" />
      <p class="muted">Mesajlar yalnızca bu bağlantıya, campaign_url değişkeniyle yönlendirir.</p>
      <IssueList :issues="issues" />
      <button type="submit" class="primary" :disabled="busy || !brief.trim()">Üret</button>
    </form>
  </section>
</template>
