import { defineConfig } from 'vitest/config'

// I test di questo scaffold coprono utility pure: nessun runtime Nuxt, quindi
// nessun ambiente browser da simulare. I test di componenti arriveranno con le
// issue di UI, tramite @nuxt/test-utils.
export default defineConfig({
  test: {
    environment: 'node',
    include: ['test/**/*.test.ts'],
  },
})
