<script setup lang="ts">
import { useHead, useRuntimeConfig, useSeoMeta } from '#imports'
import FeatureCard from '~/components/FeatureCard.vue'
import PlannerPreview from '~/components/PlannerPreview.vue'

const config = useRuntimeConfig()
const title = 'Postqron — I tuoi contenuti social, finalmente in ordine'
const description = 'Pianifica, programma e controlla i contenuti social da un unico spazio chiaro, pensato per professionisti, creator e piccoli team.'
const canonical = `${config.public.siteUrl}/`

useSeoMeta({
  title,
  description,
  ogTitle: title,
  ogDescription: description,
  ogType: 'website',
  ogUrl: canonical,
  ogImage: `${config.public.siteUrl}/og.png`,
  twitterCard: 'summary_large_image',
  twitterTitle: title,
  twitterDescription: description,
  twitterImage: `${config.public.siteUrl}/og.png`,
})
useHead({
  link: [{ rel: 'canonical', href: canonical }],
  script: [{
    type: 'application/ld+json',
    innerHTML: JSON.stringify({
      '@context': 'https://schema.org',
      '@type': 'SoftwareApplication',
      name: 'Postqron',
      applicationCategory: 'BusinessApplication',
      operatingSystem: 'Web',
      description,
      url: canonical,
    }),
  }],
})

const features = [
  {
    number: '01',
    title: 'Un calendario che parla chiaro',
    copy: 'Vedi cosa uscirà, dove e quando. Sposta, modifica o annulla un contenuto senza perdere il filo.',
  },
  {
    number: '02',
    title: 'Un contenuto, più canali',
    copy: 'Prepara il post una volta, scegli le destinazioni e correggi subito ciò che non rispetta le regole del canale.',
  },
  {
    number: '03',
    title: 'Esiti sempre visibili',
    copy: 'Sai quando un post è programmato, in pubblicazione, pubblicato o richiede il tuo intervento.',
  },
]
</script>

<template>
  <div>
    <section class="hero-section">
      <div class="content-wrap hero-section__grid">
        <div class="hero-section__copy">
          <p class="eyebrow">
            Più chiarezza. Meno schede aperte.
          </p>
          <h1>I tuoi contenuti social, finalmente in ordine.</h1>
          <p class="hero-section__lead">
            Postqron rende chiaro cosa verrà pubblicato, dove e quando. Da un
            solo spazio, senza rincorrere fogli, promemoria e bozze sparse.
          </p>
          <div class="hero-section__actions">
            <a
              class="pq-button"
              :href="config.public.appUrl"
            >Inizia ora</a>
            <NuxtLink
              class="pq-button pq-button--secondary"
              to="/funzionalita"
            >
              Scopri come funziona
            </NuxtLink>
          </div>
          <p class="hero-section__note">
            Prova Pro per 14 giorni. Decidi con calma.
          </p>
        </div>
        <PlannerPreview />
      </div>
    </section>

    <section
      class="trust-strip"
      aria-label="Pensato per il tuo modo di lavorare"
    >
      <div class="content-wrap">
        <span>Per freelance</span>
        <span>Per creator</span>
        <span>Per piccoli team</span>
        <span>Privacy by design</span>
      </div>
    </section>

    <section class="section-block content-wrap">
      <div class="section-heading">
        <p class="eyebrow">
          Tutto sotto controllo
        </p>
        <h2>Il lavoro editoriale torna a essere semplice.</h2>
        <p>
          Postqron mette ordine tra idee, canali e scadenze, così puoi dedicare
          più tempo ai contenuti e meno alla loro gestione.
        </p>
      </div>
      <div class="feature-grid">
        <FeatureCard
          v-for="feature in features"
          :key="feature.number"
          :number="feature.number"
          :title="feature.title"
        >
          {{ feature.copy }}
        </FeatureCard>
      </div>
      <NuxtLink
        class="text-link"
        to="/funzionalita"
      >
        Esplora tutte le funzionalità <span aria-hidden="true">→</span>
      </NuxtLink>
    </section>

    <section class="section-block section-block--tinted">
      <div class="content-wrap workflow">
        <div class="section-heading">
          <p class="eyebrow">
            Dal pensiero alla pubblicazione
          </p>
          <h2>Tre passaggi, nessuna sorpresa.</h2>
        </div>
        <ol class="workflow__steps">
          <li>
            <span>1</span>
            <div>
              <h3>Collega i canali</h3>
              <p>Scegli gli account che vuoi gestire e mantieni sempre chiari permessi e stato.</p>
            </div>
          </li>
          <li>
            <span>2</span>
            <div>
              <h3>Prepara e programma</h3>
              <p>Scrivi, aggiungi i media, seleziona data, ora e fuso orario.</p>
            </div>
          </li>
          <li>
            <span>3</span>
            <div>
              <h3>Controlla l’esito</h3>
              <p>Segui ogni pubblicazione e intervieni rapidamente se qualcosa non va.</p>
            </div>
          </li>
        </ol>
      </div>
    </section>

    <section class="cta-band content-wrap">
      <div>
        <p class="eyebrow">
          Il prossimo post può essere più semplice
        </p>
        <h2>Porta ordine nel tuo calendario editoriale.</h2>
      </div>
      <a
        class="pq-button"
        :href="config.public.appUrl"
      >Prova Postqron</a>
    </section>
  </div>
</template>
