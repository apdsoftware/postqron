import { defineVitestConfig } from '@nuxt/test-utils/config'

// I test girano nell'ambiente Nuxt: i componenti del design system usano gli
// import automatici (`computed`, `ref`) e i componenti globali (`NuxtLink`,
// `HexIcon`), e riprodurli a mano nel test significherebbe provare qualcosa di
// diverso da ciò che la build produce.
export default defineVitestConfig({
  test: {
    environment: 'nuxt',
    include: ['test/**/*.test.ts'],

    // L'ambiente Nuxt si prepara in un `beforeAll` di `@nuxt/test-utils`, e il
    // predefinito di Vitest per gli hook è **dieci secondi** — un numero che
    // nessuno ha scelto per questo lavoro.
    //
    // Preparare quell'ambiente non è istantaneo, e su una macchina occupata da
    // altro ci mette più del predefinito: la CI diventa rossa con
    // `Hook timed out in 10000ms` su cinque o sei file insieme, sempre diversi,
    // senza che una riga del nostro codice c'entri. È successo più volte, e ogni
    // volta la diagnosi è costata più della corsa.
    //
    // Sessanta secondi non nascondono un test lento — quelli hanno il proprio
    // `testTimeout` — ma tolgono di mezzo un rosso che misura il carico della
    // macchina invece del codice. Un ambiente che davvero non si prepara in un
    // minuto è un guasto vero, e a quel punto il rosso lo vogliamo.
    hookTimeout: 60_000,
  },
})
