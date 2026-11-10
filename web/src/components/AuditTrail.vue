<script setup lang="ts">
import type { AuditEvent } from '../api/types'
import { campaignState } from '../labels'

defineProps<{ events: AuditEvent[] }>()
</script>

<template>
  <section>
    <h2>Denetim kaydı</h2>
    <div class="table-scroll">
      <table>
        <tbody>
          <tr v-for="(e, n) in events" :key="n">
            <td>{{ new Date(e.at).toLocaleString('tr-TR') }}</td>
            <td>{{ e.from_state ? campaignState(e.from_state) : 'yok' }} → {{ campaignState(e.to_state) }}</td>
            <td>{{ e.actor }}</td>
            <td>{{ e.reason }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
