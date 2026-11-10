<script setup lang="ts">
import type { Issue, IssueMap } from '../api/issues'
import type { Draft } from '../composables/useDraft'
import IssueList from './IssueList.vue'

defineProps<{ draft: Draft; issues: IssueMap | null; warnings?: Issue[] | null; readonly?: boolean; busy?: boolean; dirty?: boolean }>()
defineEmits<{ save: [] }>()
</script>

<template>
  <section>
    <h2>Metin</h2>
    <p class="muted">Değişkenler: first_name, last_name, city, campaign_url. Bağlantı yalnızca campaign_url ile eklenir.</p>
    <label for="sms">SMS metni <span class="muted">({{ draft.sms.length }} karakter)</span></label>
    <textarea id="sms" v-model="draft.sms" rows="3" :readonly="readonly" />
    <IssueList :issues="issues?.sms" />
    <label for="subject">E-posta konusu</label>
    <input id="subject" v-model="draft.subject" type="text" :readonly="readonly" />
    <IssueList :issues="issues?.emailSubject" />
    <label for="body">E-posta gövdesi</label>
    <textarea id="body" v-model="draft.body" rows="8" :readonly="readonly" />
    <IssueList :issues="issues?.emailBody" />
    <IssueList :issues="issues?.form" />
    <IssueList :issues="warnings" warn />
    <p v-if="!readonly">
      <button type="button" class="primary" :disabled="busy || !dirty" @click="$emit('save')">Düzenlemeyi kaydet</button>
      <span v-if="dirty" class="muted">Kaydedilmemiş değişiklikler</span>
    </p>
  </section>
</template>
