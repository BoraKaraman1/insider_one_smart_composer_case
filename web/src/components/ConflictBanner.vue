<script setup lang="ts">
import { ref } from 'vue'
import type { CampaignView } from '../api/types'
import type { Draft } from '../composables/useDraft'

// A 409 on save: the draft is kept. Compare shows the server's latest copy
// next to the draft; only after a compare can the draft be saved on top.
defineProps<{ draft: Draft; latest: CampaignView | null }>()
const emit = defineEmits<{ useLatest: []; saveOnTop: [] }>()
const comparing = ref(false)

function useLatest() {
  if (confirm('Discard your edits and use the latest saved copy?')) emit('useLatest')
}
</script>

<template>
  <div class="notice">
    <p>Someone changed this campaign since you started editing. Your draft is kept.</p>
    <button type="button" @click="comparing = !comparing">Compare</button>
    <button type="button" @click="useLatest">Use latest</button>
    <button v-if="comparing" type="button" @click="emit('saveOnTop')">Save on top of latest</button>
    <div v-if="comparing" class="compare">
      <div>
        <h2>Latest saved</h2>
        <template v-if="latest?.revision">
          <p><strong>SMS</strong><br />{{ latest.revision.sms_body }}</p>
          <p><strong>Subject</strong><br />{{ latest.revision.email_subject }}</p>
          <p><strong>Body</strong><br />{{ latest.revision.email_body }}</p>
        </template>
        <p v-else class="muted">No saved revision.</p>
      </div>
      <div>
        <h2>Your draft</h2>
        <p><strong>SMS</strong><br />{{ draft.sms }}</p>
        <p><strong>Subject</strong><br />{{ draft.subject }}</p>
        <p><strong>Body</strong><br />{{ draft.body }}</p>
      </div>
    </div>
  </div>
</template>
