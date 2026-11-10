<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, toRef, watch } from 'vue'
import type { CampaignView } from '../api/types'
import { useDeliveryPolling } from '../composables/useDeliveryPolling'
import { channelName, messageState } from '../labels'
import { api, clearToken } from '../session'
import AttentionTable from './AttentionTable.vue'

const props = defineProps<{ campaignId: string }>()
const emit = defineEmits<{ view: [CampaignView] }>()

const p = useDeliveryPolling(toRef(props, 'campaignId'), api, { onUnauthorized: clearToken })
watch(p.view, (v) => v && emit('view', v))

const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined
onMounted(() => (clock = setInterval(() => (now.value = Date.now()), 1000)))
onUnmounted(() => clearInterval(clock))

const ago = computed(() => (p.lastUpdated.value === null ? null : Math.max(0, Math.round((now.value - p.lastUpdated.value) / 1000))))

const colors: Record<string, string> = {
  sent: '#2e7d32',
  sending: '#1976d2',
  queued: '#90a4ae',
  retrying: '#f9a825',
  failed: '#c62828',
  dead: '#6a1b9a',
  skipped: '#bdbdbd',
}
const states = Object.keys(colors)

function total(c: Record<string, number>) {
  return Object.values(c).reduce((a, b) => a + b, 0)
}
</script>

<template>
  <section>
    <h2>Teslimat</h2>
    <p class="muted">
      <span v-if="ago !== null">Son güncelleme {{ ago }} sn önce.</span>
      <span v-if="p.stale.value" class="error"> Eski veri: son güncelleme başarısız, yeniden deneniyor.</span>
      <span v-if="!p.polling.value"> Bitti, artık sorgulanmıyor.</span>
    </p>
    <div v-for="(counts, channel) in p.stats.value?.channels ?? {}" :key="channel">
      <strong>{{ channelName(channel) }}</strong> <span class="muted">{{ total(counts) }} mesaj</span>
      <div class="bar">
        <span
          v-for="s in states"
          :key="s"
          :title="`${messageState(s)}: ${counts[s] ?? 0}`"
          :style="{ width: total(counts) ? ((counts[s] ?? 0) * 100) / total(counts) + '%' : '0', background: colors[s] }"
        />
      </div>
      <div class="table-scroll">
        <table>
          <tbody>
            <tr>
              <td v-for="s in states" :key="s">{{ messageState(s) }}: {{ counts[s] ?? 0 }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
    <AttentionTable :items="p.attention.value" />
  </section>
</template>
