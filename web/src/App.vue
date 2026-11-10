<script setup lang="ts">
import TokenGate from './components/TokenGate.vue'
import { clearToken, token } from './session'
</script>

<template>
  <header>
    <RouterLink to="/" class="brand">Smart Campaign Composer</RouterLink>
    <nav v-if="token">
      <RouterLink v-if="$route.path !== '/'" to="/" class="button primary">New campaign</RouterLink>
      <button type="button" class="link" @click="clearToken">Sign out</button>
    </nav>
  </header>
  <main>
    <TokenGate v-if="!token" />
    <RouterView v-else :key="$route.path" />
  </main>
</template>
