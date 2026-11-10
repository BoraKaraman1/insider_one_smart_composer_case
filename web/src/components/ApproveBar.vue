<script setup lang="ts">
import type { CampaignState } from '../api/types'
import { isFinished } from '../labels'

defineProps<{ state: CampaignState; busy: boolean; dirty: boolean }>()
defineEmits<{ approve: []; reject: []; regenerate: []; cancel: [] }>()
</script>

<template>
  <section v-if="!isFinished(state)" class="actions">
    <button v-if="state === 'in_review'" type="button" class="primary" :disabled="busy || dirty" @click="$emit('approve')">Approve</button>
    <button v-if="state === 'in_review'" type="button" class="danger" :disabled="busy" @click="$emit('reject')">Reject</button>
    <button
      v-if="state === 'in_review' || state === 'generation_failed' || state === 'draft'"
      type="button"
      :disabled="busy"
      @click="$emit('regenerate')"
    >
      {{ state === 'draft' ? 'Generate' : 'Regenerate' }}
    </button>
    <button
      v-if="['draft', 'generating', 'in_review', 'generation_failed', 'approved', 'sending'].includes(state)"
      type="button"
      class="danger"
      :disabled="busy"
      @click="$emit('cancel')"
    >
      Cancel campaign
    </button>
    <span v-if="state === 'in_review' && dirty" class="muted">Save your edits before approving.</span>
  </section>
</template>
