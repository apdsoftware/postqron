<script setup lang="ts">
import { useHead, useRuntimeConfig, useSeoMeta } from '#imports'

const config = useRuntimeConfig()
const title = 'Funzionalità — Postqron'
const description = 'Dalla bozza al calendario e all’esito di pubblicazione: scopri come Postqron semplifica la gestione dei contenuti social.'
const canonical = `${config.public.siteUrl}/funzionalita`

useSeoMeta({
  title,
  description,
  ogTitle: title,
  ogDescription: description,
  ogUrl: canonical,
  ogImage: `${config.public.siteUrl}/og.png`,
  twitterCard: 'summary_large_image',
})
useHead({ link: [{ rel: 'canonical', href: canonical }] })

const capabilities = [
  {
    eyebrow: 'Pianifica',
    title: 'Il calendario editoriale che ti fa vedere davvero avanti.',
    copy: 'Contenuti, canali, date e stati convivono in una vista ordinata. Puoi modificare, duplicare, riprogrammare o annullare ciò che non è ancora partito.',
    points: ['Data, ora e fuso orario espliciti', 'Vista per giorno e stato', 'Modifiche prima della pubblicazione'],
  },
  {
    eyebrow: 'Crea',
    title: 'Scrivi una volta, adatta con consapevolezza.',
    copy: 'Il composer raccoglie testo, media e destinazioni. Prima di programmare, Postqron segnala i vincoli specifici dei canali selezionati.',
    points: ['Bozze sempre recuperabili', 'Validazione per ogni canale', 'Media e testo nello stesso flusso'],
  },
  {
    eyebrow: 'Controlla',
    title: 'L’esito non resta nascosto in una coda.',
    copy: 'Ogni contenuto mostra uno stato comprensibile. Se una pubblicazione fallisce, trovi una causa utile e puoi riprovare in modo controllato.',
    points: ['Stati chiari e coerenti', 'Nessuna pubblicazione duplicata', 'Avvisi quando serve intervenire'],
  },
]
</script>

<template>
  <div>
    <section class="page-hero content-wrap">
      <p class="eyebrow">
        Funzionalità
      </p>
      <h1>Un flusso unico, dalla prima idea all’ultimo controllo.</h1>
      <p>
        Meno passaggi manuali, più visibilità. Postqron accompagna il contenuto
        senza nascondere scelte, vincoli o risultati.
      </p>
    </section>

    <section class="capability-list content-wrap">
      <article
        v-for="(capability, index) in capabilities"
        :key="capability.title"
        class="capability"
      >
        <div
          class="capability__visual"
          aria-hidden="true"
        >
          <span>{{ String(index + 1).padStart(2, '0') }}</span>
          <i />
          <i />
          <i />
        </div>
        <div>
          <p class="eyebrow">
            {{ capability.eyebrow }}
          </p>
          <h2>{{ capability.title }}</h2>
          <p>{{ capability.copy }}</p>
          <ul>
            <li
              v-for="point in capability.points"
              :key="point"
            >
              {{ point }}
            </li>
          </ul>
        </div>
      </article>
    </section>

    <section class="cta-band content-wrap">
      <div>
        <p class="eyebrow">
          Inizia dal prossimo contenuto
        </p>
        <h2>Costruisci un calendario più leggibile.</h2>
      </div>
      <a
        class="pq-button"
        :href="config.public.appUrl"
      >Inizia ora</a>
    </section>
  </div>
</template>
