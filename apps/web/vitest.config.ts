import { defineVitestConfig } from '@nuxt/test-utils/config'

// I test girano nell'ambiente Nuxt: i componenti del design system usano gli
// import automatici (`computed`, `ref`) e i componenti globali (`NuxtLink`,
// `HexIcon`), e riprodurli a mano nel test significherebbe provare qualcosa di
// diverso da ciò che la build produce.
export default defineVitestConfig({
  test: {
    environment: 'nuxt',
    include: ['test/**/*.test.ts'],
  },
})
