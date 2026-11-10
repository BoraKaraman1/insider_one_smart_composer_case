<script setup lang="ts">
import type { CampaignState } from '../api/types'

defineProps<{ state: CampaignState; busy: boolean; dirty: boolean }>()
defineEmits<{ approve: []; reject: []; regenerate: []; cancel: [] }>()
</script>

<template>
  <section>
    <button v-if="state === 'in_review'" type="button" class="primary" :disabled="busy || dirty" @click="$emit('approve')">Onayla</button>
    <button v-if="state === 'in_review'" type="button" class="danger" :disabled="busy" @click="$emit('reject')">Reddet</button>
    <button
      v-if="state === 'in_review' || state === 'generation_failed' || state === 'draft'"
      type="button"
      :disabled="busy"
      @click="$emit('regenerate')"
    >
      {{ state === 'draft' ? 'Üret' : 'Yeniden üret' }}
    </button>
    <button
      v-if="['draft', 'generating', 'in_review', 'generation_failed', 'approved', 'sending'].includes(state)"
      type="button"
      class="danger"
      :disabled="busy"
      @click="$emit('cancel')"
    >
      Kampanyayı iptal et
    </button>
    <span v-if="state === 'in_review' && dirty" class="muted">Onaylamadan önce düzenlemelerinizi kaydedin veya bırakın.</span>
  </section>
</template>
