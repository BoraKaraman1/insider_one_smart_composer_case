import { defineConfig } from 'vitest/config'

// The nginx scenarios (plan 1.7). They run against the compose stack on
// :8081 in mock mode, so they are kept out of npm test.
export default defineConfig({
  test: {
    include: ['e2e/**/*.e2e.test.ts'],
    environment: 'node',
    fileParallelism: false,
  },
})
