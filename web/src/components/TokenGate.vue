<script setup lang="ts">
import { ref } from 'vue'
import { setToken } from '../session'

const value = ref('')

function submit() {
  const t = value.value.trim()
  if (t) setToken(t)
}
</script>

<template>
  <section>
    <h2>Yönetici anahtarı</h2>
    <form @submit.prevent="submit">
      <label for="token">Anahtar</label>
      <input id="token" v-model="value" type="password" autocomplete="off" />
      <p class="muted">Bu sekmenin oturum belleğinde tutulur, yalnızca Authorization başlığıyla gönderilir.</p>
      <button type="submit" class="primary" :disabled="!value.trim()">Devam et</button>
    </form>
  </section>
</template>
