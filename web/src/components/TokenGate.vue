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
    <h2>Admin token</h2>
    <form @submit.prevent="submit">
      <label for="token">Token</label>
      <input id="token" v-model="value" type="password" autocomplete="off" />
      <p class="muted">Kept in this tab's session storage and sent only in the Authorization header.</p>
      <button type="submit" :disabled="!value.trim()">Continue</button>
    </form>
  </section>
</template>
