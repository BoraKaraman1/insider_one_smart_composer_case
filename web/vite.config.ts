import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

const api = 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': api,
      '/healthz': api,
      '/readyz': api,
    },
  },
  test: {
    include: ['src/**/*.test.ts'],
    environment: 'node',
  },
})
