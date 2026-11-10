<script setup lang="ts">
import TokenGate from './components/TokenGate.vue'
import { clearToken, token } from './session'
</script>

<template>
  <header>
    <RouterLink to="/" class="brand">Smart Campaign Composer</RouterLink>
    <button v-if="token" type="button" class="link" @click="clearToken">Çıkış yap</button>
  </header>
  <main>
    <TokenGate v-if="!token" />
    <RouterView v-else :key="$route.path" />
  </main>
</template>
