<script setup lang="ts">
import type { Issue } from '../api/issues'

defineProps<{ issues: Issue[] | null | undefined; warn?: boolean }>()
</script>

<template>
  <ul v-if="issues && issues.length" class="issues" :class="{ warn }">
    <li v-for="(i, n) in issues" :key="n">
      <code>{{ i.code }}</code> {{ i.message }}<span v-if="i.path" class="muted"> ({{ i.path }})</span>
    </li>
  </ul>
</template>
