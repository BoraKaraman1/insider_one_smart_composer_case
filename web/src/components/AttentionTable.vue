<script setup lang="ts">
import type { AttentionItem } from '../api/types'

defineProps<{ items: AttentionItem[] }>()
</script>

<template>
  <div>
    <h2>Needs attention</h2>
    <p v-if="!items.length" class="muted">Nothing stuck or failed.</p>
    <table v-else>
      <thead>
        <tr><th>Message</th><th>Channel</th><th>State</th><th>Reason</th><th>Attempts</th><th>Last error</th><th>Next attempt</th></tr>
      </thead>
      <tbody>
        <tr v-for="m in items" :key="m.id">
          <td><code>{{ m.id.slice(0, 8) }}</code></td>
          <td>{{ m.channel }}</td>
          <td>{{ m.state }}</td>
          <td>{{ m.reason }}</td>
          <td>{{ m.attempts }}</td>
          <td>{{ m.last_error_class ?? '' }} {{ m.last_error ?? '' }}</td>
          <td>{{ m.next_attempt_at ?? '' }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
