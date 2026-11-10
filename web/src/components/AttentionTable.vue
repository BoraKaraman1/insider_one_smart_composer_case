<script setup lang="ts">
import type { AttentionItem } from '../api/types'
import { channelName, messageState } from '../labels'

defineProps<{ items: AttentionItem[] }>()
</script>

<template>
  <div>
    <h2>İlgi gerekiyor</h2>
    <p v-if="!items.length" class="muted">Takılan veya başarısız mesaj yok.</p>
    <div v-else class="table-scroll">
    <table>
      <thead>
        <tr><th>Mesaj</th><th>Kanal</th><th>Durum</th><th>Neden</th><th>Deneme</th><th>Son hata</th><th>Sonraki deneme</th></tr>
      </thead>
      <tbody>
        <tr v-for="m in items" :key="m.id">
          <td><code>{{ m.id.slice(0, 8) }}</code></td>
          <td>{{ channelName(m.channel) }}</td>
          <td>{{ messageState(m.state) }}</td>
          <td>{{ m.reason }}</td>
          <td>{{ m.attempts }}</td>
          <td>{{ m.last_error_class ?? '' }} {{ m.last_error ?? '' }}</td>
          <td>{{ m.next_attempt_at ?? '' }}</td>
        </tr>
      </tbody>
    </table>
    </div>
  </div>
</template>
