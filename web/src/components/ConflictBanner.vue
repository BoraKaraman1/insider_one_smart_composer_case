<script setup lang="ts">
import { ref } from 'vue'
import type { CampaignView } from '../api/types'
import type { Draft } from '../composables/useDraft'

defineProps<{ draft: Draft; latest: CampaignView | null }>()
const emit = defineEmits<{ useLatest: []; saveOnTop: [] }>()
const comparing = ref(false)

function useLatest() {
  if (confirm('Düzenlemeleriniz silinsin ve son kaydedilen kopya kullanılsın mı?')) emit('useLatest')
}
</script>

<template>
  <div class="notice">
    <p>Düzenlemeye başladığınızdan beri biri bu kampanyayı değiştirdi. Taslağınız korundu.</p>
    <button type="button" @click="comparing = !comparing">Karşılaştır</button>
    <button type="button" @click="useLatest">Sonuncuyu kullan</button>
    <button v-if="comparing" type="button" @click="emit('saveOnTop')">Sonuncunun üstüne kaydet</button>
    <div v-if="comparing" class="compare">
      <div>
        <h2>Son kaydedilen</h2>
        <template v-if="latest?.revision">
          <p><strong>SMS</strong><br />{{ latest.revision.sms_body }}</p>
          <p><strong>Konu</strong><br />{{ latest.revision.email_subject }}</p>
          <p><strong>Gövde</strong><br />{{ latest.revision.email_body }}</p>
        </template>
        <p v-else class="muted">Kaydedilmiş revizyon yok.</p>
      </div>
      <div>
        <h2>Taslağınız</h2>
        <p><strong>SMS</strong><br />{{ draft.sms }}</p>
        <p><strong>Konu</strong><br />{{ draft.subject }}</p>
        <p><strong>Gövde</strong><br />{{ draft.body }}</p>
      </div>
    </div>
  </div>
</template>
