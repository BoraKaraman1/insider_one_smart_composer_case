import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

// In dev, Vite serves the app and proxies the API, so the browser sees one
// origin as it does behind nginx.
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
    // Unit specs only. The e2e scenarios need the compose stack: npm run e2e.
    include: ['src/**/*.test.ts'],
    environment: 'node',
  },
})
