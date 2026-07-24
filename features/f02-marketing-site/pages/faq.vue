<script setup lang="ts">
import { useHead, useRuntimeConfig, useSeoMeta } from '#imports'

const config = useRuntimeConfig()
const title = 'Domande frequenti — Postqron'
const description = 'Risposte essenziali su prova, piani, canali, programmazione, sicurezza e controllo dei cookie in Postqron.'
const canonical = `${config.public.siteUrl}/faq`

const questions = [
  {
    question: 'Posso provare Postqron prima di scegliere un piano?',
    answer: 'Sì. Al primo accesso puoi usare il piano Pro per 14 giorni e verificare come si inserisce nel tuo flusso di lavoro.',
  },
  {
    question: 'Cosa succede quando raggiungo un limite del piano?',
    answer: 'Postqron non elimina contenuti o collegamenti. Blocca soltanto la nuova azione che supera il limite e ti mostra quale capacità è esaurita.',
  },
  {
    question: 'Posso cambiare la data di un post già programmato?',
    answer: 'Sì, finché il post non è entrato in pubblicazione puoi modificarlo, riprogrammarlo o annullarlo.',
  },
  {
    question: 'Come capisco se un post è stato pubblicato?',
    answer: 'Ogni destinazione mostra il proprio stato. In caso di errore ricevi una spiegazione utile e puoi intervenire senza creare duplicati.',
  },
  {
    question: 'I miei account social restano sotto il mio controllo?',
    answer: 'Sì. Puoi vedere quali account sono collegati e revocare l’accesso. Postqron usa i permessi necessari alle funzioni che scegli.',
  },
  {
    question: 'Posso rifiutare i cookie non necessari?',
    answer: 'Sì. Accettazione, rifiuto e personalizzazione hanno la stessa evidenza. Puoi cambiare scelta in ogni momento dal footer.',
  },
]

useSeoMeta({
  title,
  description,
  ogTitle: title,
  ogDescription: description,
  ogUrl: canonical,
  ogImage: `${config.public.siteUrl}/og.png`,
  twitterCard: 'summary_large_image',
})
useHead({
  link: [{ rel: 'canonical', href: canonical }],
  script: [{
    type: 'application/ld+json',
    innerHTML: JSON.stringify({
      '@context': 'https://schema.org',
      '@type': 'FAQPage',
      mainEntity: questions.map(item => ({
        '@type': 'Question',
        name: item.question,
        acceptedAnswer: {
          '@type': 'Answer',
          text: item.answer,
        },
      })),
    }),
  }],
})
</script>

<template>
  <div>
    <section class="page-hero content-wrap">
      <p class="eyebrow">
        FAQ
      </p>
      <h1>Le risposte che servono, senza giri di parole.</h1>
      <p>
        Una panoramica rapida su prova, limiti, pubblicazione, sicurezza e
        privacy.
      </p>
    </section>

    <section class="faq-list content-wrap">
      <details
        v-for="(item, index) in questions"
        :key="item.question"
        :open="index === 0"
      >
        <summary>
          <span>{{ item.question }}</span>
          <span aria-hidden="true">+</span>
        </summary>
        <p>{{ item.answer }}</p>
      </details>
    </section>

    <section class="cta-band content-wrap">
      <div>
        <p class="eyebrow">
          Pronto a fare ordine?
        </p>
        <h2>Prova Postqron con i tuoi contenuti.</h2>
      </div>
      <a
        class="pq-button"
        :href="config.public.appUrl"
      >Inizia ora</a>
    </section>
  </div>
</template>
