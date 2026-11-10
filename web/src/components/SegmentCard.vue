<script setup lang="ts">
import type { Issue } from '../api/issues'
import type { SegmentCounts } from '../api/types'
import IssueList from './IssueList.vue'

defineProps<{
  rule: unknown
  description?: string | null
  counts?: Partial<SegmentCounts> | null
  issues?: Issue[]
}>()
</script>

<template>
  <section>
    <h2>Segment</h2>
    <p v-if="description">{{ description }}</p>
    <p v-if="counts" class="muted">
      Kitle {{ counts.total ?? '?' }}, SMS uygun {{ counts.sms_eligible ?? '?' }}, e-posta uygun
      {{ counts.email_eligible ?? '?' }}
    </p>
    <details>
      <summary>Kural</summary>
      <pre>{{ JSON.stringify(rule, null, 2) }}</pre>
    </details>
    <IssueList :issues="issues" />
  </section>
</template>
