<script setup lang="ts">
import { computed, onMounted, toRef } from 'vue'
import type { Issue } from '../api/issues'
import type { CampaignView, SegmentReady } from '../api/types'
import ApproveBar from '../components/ApproveBar.vue'
import AuditTrail from '../components/AuditTrail.vue'
import ConflictBanner from '../components/ConflictBanner.vue'
import CopyEditor from '../components/CopyEditor.vue'
import DeliveryDashboard from '../components/DeliveryDashboard.vue'
import IssueList from '../components/IssueList.vue'
import SegmentCard from '../components/SegmentCard.vue'
import { useCampaign } from '../composables/useCampaign'
import { useDraft } from '../composables/useDraft'
import { useGeneration } from '../composables/useGeneration'
import { campaignState } from '../labels'
import { api, clearToken, token } from '../session'

const props = defineProps<{ id: string }>()
const c = useCampaign(toRef(props, 'id'), api)
const d = useDraft()
const g = useGeneration({
  token: () => token.value ?? '',
  getCampaign: (id, signal) => api.getCampaign(id, signal),
  onUnauthorized: clearToken,
})

d.track(c.view)

const campaign = computed(() => c.view.value?.campaign ?? null)
const generating = computed(() => campaign.value?.state === 'generating')
const streamed = computed(() => g.state.value.segment as SegmentReady | null)
const delivery = computed(() => campaign.value?.approved_revision_id != null)
const editable = computed(() => campaign.value?.state === 'in_review')

let replaceDraft = false

async function followGeneration(eventsUrl: string) {
  await g.follow(props.id, eventsUrl)
  const v = g.view.value
  if (!v) return
  c.view.value = v
  if (replaceDraft) {
    replaceDraft = false
    d.useLatest(v)
  }
}

onMounted(async () => {
  const v = await c.reload()
  // Başarısız üretimin olayları yeniden okunur ki hata ayrıntısı yenilemeden sonra da görünsün.
  const state = v?.campaign.state
  if ((state === 'generating' || state === 'generation_failed') && v?.generation) void followGeneration(v.generation.events_url)
})

function setView(v: CampaignView | null) {
  if (v) c.view.value = v
}

async function save() {
  const r = await c.act('Kaydetme', () => d.save((body) => api.saveRevision(props.id, body)))
  // Görünüm taslağın tabanı yenilendikten sonra değişir ki izleyici bunu çakışma sanmasın.
  if (r === 'saved') setView(d.latest.value)
  if (r === 'conflict') await c.reload()
}

async function saveOnTop() {
  const latest = (await c.reload()) ?? d.latest.value
  if (!latest) return
  d.rebase(latest)
  await save()
}

function useLatest() {
  if (d.latest.value) d.useLatest(d.latest.value)
}

async function approve() {
  const v = campaign.value
  const shown = d.approval()
  if (!v || !shown) return
  setView(await c.act('Onaylama', () => api.approve(props.id, shown, v.version)))
}

async function reject() {
  if (!campaign.value || !confirm('Bu kampanya reddedilsin mi? Yeniden açılamaz.')) return
  const version = campaign.value.version
  setView(await c.act('Reddetme', () => api.reject(props.id, version)))
}

async function cancel() {
  if (!campaign.value || !confirm('Bu kampanya iptal edilsin mi? Henüz gönderilmeyen mesajlar atlanır.')) return
  const version = campaign.value.version
  const v = await c.act('İptal', () => api.cancel(props.id, version))
  if (v) {
    g.stop()
    setView(v)
  }
}

async function regenerate() {
  if (!campaign.value) return
  if (d.dirty.value && !confirm('Yeniden üretmek kaydedilmemiş düzenlemelerinizi siler. Devam edilsin mi?')) return
  const version = campaign.value.version
  const r = await c.act('Yeniden üretme', () => api.generate(props.id, version))
  if (!r) return
  replaceDraft = true
  await c.reload()
  void followGeneration(r.events_url)
}
</script>

<template>
  <p v-if="c.error.value" class="error">{{ c.error.value }}</p>
  <p v-if="c.notice.value" class="notice">{{ c.notice.value }}</p>
  <template v-if="campaign">
    <section>
      <h2>Kampanya <span class="muted">{{ campaignState(campaign.state) }}, sürüm {{ campaign.version }}</span></h2>
      <p>{{ campaign.brief }}</p>
      <p class="muted">Bağlantılar şuraya gider: {{ campaign.campaign_url }}</p>
    </section>

    <ApproveBar :state="campaign.state" :busy="c.busy.value" :dirty="!d.approvable.value" @approve="approve" @reject="reject" @regenerate="regenerate" @cancel="cancel" />

    <p v-if="g.endedEarly.value" class="notice">Üretim akışı erken bitti. Kampanya kayıtlı haliyle gösteriliyor.</p>
    <p v-if="g.error.value" class="error">{{ g.error.value }}</p>

    <template v-if="generating">
      <SegmentCard v-if="streamed" :rule="streamed.rule" :description="streamed.description" :counts="streamed.counts" />
      <p v-else class="muted">Segment seçiliyor...</p>
      <IssueList :issues="g.state.value.errors as Issue[]" />
      <section class="provisional">
        <h2>Metin <span class="muted">geçici, deneme {{ g.state.value.attempt || 1 }}</span></h2>
        <p><strong>SMS</strong><br />{{ g.state.value.fields['sms.body'] ?? '' }}</p>
        <p><strong>Konu</strong><br />{{ g.state.value.fields['email.subject'] ?? '' }}</p>
        <p><strong>Gövde</strong><br />{{ g.state.value.fields['email.body'] ?? '' }}</p>
      </section>
    </template>

    <template v-else>
      <IssueList v-if="campaign.state === 'generation_failed'" :issues="g.state.value.errors as Issue[]" />
      <template v-if="c.view.value?.revision">
        <SegmentCard
          :rule="c.view.value.revision.segment"
          :description="streamed?.description"
          :counts="{
            total: c.view.value.revision.audience_total ?? undefined,
            sms_eligible: c.view.value.revision.sms_eligible ?? undefined,
            email_eligible: c.view.value.revision.email_eligible ?? undefined,
          }"
          :issues="d.issues.value?.segment"
        />
        <ConflictBanner v-if="d.conflict.value && d.draft.value" :draft="d.draft.value" :latest="d.latest.value" @use-latest="useLatest" @save-on-top="saveOnTop" />
        <CopyEditor
          v-if="d.draft.value"
          :draft="d.draft.value"
          :issues="d.issues.value"
          :warnings="c.view.value.revision.warnings"
          :readonly="!editable"
          :busy="c.busy.value"
          :dirty="d.dirty.value"
          @save="save"
        />
      </template>
    </template>

    <DeliveryDashboard v-if="delivery" :campaign-id="campaign.id" @view="setView" />
    <AuditTrail :events="c.view.value?.events ?? []" />
  </template>
</template>
