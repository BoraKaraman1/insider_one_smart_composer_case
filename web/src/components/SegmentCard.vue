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
      Audience {{ counts.total ?? '?' }}, SMS eligible {{ counts.sms_eligible ?? '?' }}, email eligible
      {{ counts.email_eligible ?? '?' }}
    </p>
    <details>
      <summary>Rule</summary>
      <pre>{{ JSON.stringify(rule, null, 2) }}</pre>
    </details>
    <IssueList :issues="issues" />
  </section>
</template>
