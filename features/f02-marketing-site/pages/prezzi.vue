<script setup lang="ts">
import {
  useFetch,
  useHead,
  useRuntimeConfig,
  useSeoMeta,
} from '#imports'
import { computed } from 'vue'
import PlanCatalog from '~/components/PlanCatalog.vue'
import { parsePublicCatalog } from '~/src/catalog'

const config = useRuntimeConfig()
const title = 'Prezzi e piani — Postqron'
const description = 'Tre piani chiari per freelance, professionisti e piccoli team. Confronta membri, canali e post programmati.'
const canonical = `${config.public.siteUrl}/prezzi`

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

const {
  data: rawCatalog,
  error,
  refresh,
  status,
} = await useFetch('/api/plans', { key: 'public-plan-catalog' })

const catalog = computed(() => {
  if (!rawCatalog.value) {
    return undefined
  }
  try {
    return parsePublicCatalog(rawCatalog.value)
  } catch {
    return undefined
  }
})
</script>

<template>
  <div>
    <section class="page-hero page-hero--centered content-wrap">
      <p class="eyebrow">
        Prezzi
      </p>
      <h1>Scegli lo spazio giusto per il tuo lavoro.</h1>
      <p>
        Piani leggibili, capacità esplicite e una prova Pro di 14 giorni.
        Puoi decidere dopo aver visto Postqron nel tuo flusso reale.
      </p>
    </section>

    <section class="pricing-section content-wrap">
      <PlanCatalog
        v-if="catalog"
        :catalog="catalog"
      />
      <div
        v-else
        class="catalog-state"
        role="status"
      >
        <h2>
          {{ status === 'pending' ? 'Caricamento dei piani…' : 'Prezzi momentaneamente non disponibili' }}
        </h2>
        <p v-if="error">
          Non mostriamo valori non aggiornati. Riprova tra poco per consultare il
          catalogo ufficiale.
        </p>
        <button
          v-if="error"
          class="pq-button pq-button--secondary"
          type="button"
          @click="() => refresh()"
        >
          Riprova
        </button>
      </div>
    </section>

    <section class="pricing-note content-wrap">
      <h2>Tutti i piani includono</h2>
      <ul>
        <li>Calendario editoriale</li>
        <li>Bozze e programmazione</li>
        <li>Stato delle pubblicazioni</li>
        <li>Controlli privacy e sicurezza</li>
      </ul>
      <p>
        Hai ancora dubbi?
        <NuxtLink to="/faq">
          Consulta le domande frequenti.
        </NuxtLink>
      </p>
    </section>
  </div>
</template>
