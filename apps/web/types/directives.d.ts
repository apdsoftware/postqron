import type { Directive } from 'vue'

import type { RevealOptions } from '~/utils/reveal'

/**
 * Le direttive registrate da un plugin non sono note al compilatore dei
 * template: senza questa dichiarazione `vue-tsc` segnalerebbe `v-reveal` come
 * sconosciuta, e le sue opzioni non sarebbero controllate.
 */
declare module 'vue' {
  interface GlobalDirectives {
    vReveal: Directive<HTMLElement, RevealOptions | undefined>
  }
}

export {}
