<script setup lang="ts">
import type { AuditEvent } from '../api/types'

defineProps<{ events: AuditEvent[] }>()
</script>

<template>
  <section>
    <h2>Audit trail</h2>
    <table>
      <tbody>
        <tr v-for="(e, n) in events" :key="n">
          <td>{{ new Date(e.at).toLocaleString() }}</td>
          <td>{{ e.from_state ?? 'none' }} to {{ e.to_state }}</td>
          <td>{{ e.actor }}</td>
          <td>{{ e.reason }}</td>
        </tr>
      </tbody>
    </table>
  </section>
</template>
