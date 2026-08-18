<script setup lang="ts">
/**
 * Radice: smistamento, non contenuto (SPEC §8-bis).
 *
 * ## Cosa fa questa pagina
 *
 * Niente, ed è deliberato in due sensi diversi.
 *
 * Non ha **contenuto proprio**, come la radice del sito pubblico: se ne avesse,
 * sarebbe una sesta variante della panoramica da tradurre e tenere allineata
 * alle altre cinque. Per lo stesso motivo non ha layout — barra superiore e
 * navigazione sono in una lingua, e qui la lingua non è ancora decisa.
 *
 * E non **renderizza mai**, perché lo smistamento vero non è qui: è in
 * `middleware/01.locale.global.ts`, che gira prima che l'applicazione si monti
 * e reindirizza qualunque indirizzo privo di prefisso — `/` compreso. È la
 * differenza dal sito pubblico, dove il redirect avviene in `onMounted()`
 * perché quella pagina è un file HTML pre-renderizzato e servito così com'è.
 *
 * ## Perché allora esiste
 *
 * Perché il router deve avere una rotta da abbinare a `/`. Tutti gli altri
 * percorsi corrispondono a `pages/[locale]/`, anche quando il primo segmento
 * non è una lingua — `/jobs/42` combacia con `[locale]/[...slug]` e vale
 * `locale = "jobs"`, che il middleware riconosce come non-lingua e prefissa.
 * `/` è l'unico percorso che non ha nemmeno un segmento: senza questo file
 * Nuxt non troverebbe nessuna rotta e mostrerebbe la propria pagina d'errore,
 * cioè un guasto al posto dello smistamento.
 *
 * Il `<div>` qui sotto tiene il colore di sfondo per l'istante fra il primo
 * disegno e il redirect, che altrimenti sarebbe un lampo bianco fra una pagina
 * scura e l'altra. Nel percorso normale non si vede.
 */
definePageMeta({ layout: false })
</script>

<template>
  <div class="min-h-screen bg-gray-50 dark:bg-gray-900" />
</template>
