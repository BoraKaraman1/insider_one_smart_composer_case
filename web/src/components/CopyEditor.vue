<script setup lang="ts">
import type { Issue, IssueMap } from '../api/issues'
import type { Draft } from '../composables/useDraft'
import IssueList from './IssueList.vue'

// The draft object is owned by useDraft; the inputs edit its fields.
defineProps<{ draft: Draft; issues: IssueMap | null; warnings?: Issue[] | null; readonly?: boolean; busy?: boolean; dirty?: boolean }>()
defineEmits<{ save: [] }>()
</script>

<template>
  <section>
    <h2>Copy</h2>
    <p class="muted">Variables: first_name, last_name, city, campaign_url. Links only through campaign_url.</p>
    <label for="sms">SMS body <span class="muted">({{ draft.sms.length }} characters)</span></label>
    <textarea id="sms" v-model="draft.sms" rows="3" :readonly="readonly" />
    <IssueList :issues="issues?.sms" />
    <label for="subject">Email subject</label>
    <input id="subject" v-model="draft.subject" type="text" :readonly="readonly" />
    <IssueList :issues="issues?.emailSubject" />
    <label for="body">Email body</label>
    <textarea id="body" v-model="draft.body" rows="8" :readonly="readonly" />
    <IssueList :issues="issues?.emailBody" />
    <IssueList :issues="issues?.form" />
    <IssueList :issues="warnings" warn />
    <p v-if="!readonly">
      <button type="button" :disabled="busy || !dirty" @click="$emit('save')">Save edit</button>
      <span v-if="dirty" class="muted">Unsaved changes</span>
    </p>
  </section>
</template>
