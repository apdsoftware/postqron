<script setup lang="ts">
import { ref } from 'vue'
import {
  PqAlert,
  PqButton,
  PqCard,
  PqField,
  PqLogo,
  PqSkipLink,
} from '../components'
import '../components/components.css'

const channelName = ref('')
const showAlert = ref(true)
</script>

<template>
  <div class="brand-showcase">
    <PqSkipLink />
    <header class="brand-showcase__header">
      <PqLogo />
    </header>
    <main
      id="main-content"
      class="brand-showcase__main"
      tabindex="-1"
    >
      <div class="brand-showcase__intro">
        <p class="brand-showcase__eyebrow">
          Design system
        </p>
        <h1>Chiaro in ogni stato.</h1>
        <p>
          Componenti e token Postqron per creare esperienze coerenti,
          accessibili e responsive.
        </p>
      </div>

      <PqAlert
        v-if="showAlert"
        title="Canale da riconnettere"
        tone="warning"
        dismissible
        @dismiss="showAlert = false"
      >
        Ricollega il canale LinkedIn prima della prossima pubblicazione.
      </PqAlert>

      <section
        class="brand-showcase__grid"
        aria-label="Esempi di componenti"
      >
        <PqCard
          title="Nuovo canale"
          :heading-level="2"
        >
          <PqField
            v-model="channelName"
            label="Nome del canale"
            help="Usa un nome riconoscibile dal tuo team."
            autocomplete="organization"
            required
          />
          <template #footer>
            <PqButton variant="secondary">
              Annulla
            </PqButton>
            <PqButton>
              Collega canale
            </PqButton>
          </template>
        </PqCard>

        <PqCard
          title="Azioni"
          :heading-level="2"
        >
          <p>
            Le etichette descrivono il risultato e gli stati restano visibili
            senza affidarsi al solo colore.
          </p>
          <template #footer>
            <PqButton
              variant="danger"
              responsive
            >
              Scollega canale
            </PqButton>
          </template>
        </PqCard>
      </section>
    </main>
  </div>
</template>

<style scoped>
.brand-showcase {
  min-height: 100vh;
  color: var(--pq-color-text);
  background: var(--pq-color-canvas);
  font-family: var(--pq-font-sans);
}

.brand-showcase__header,
.brand-showcase__main {
  width: min(var(--pq-size-content), calc(100% - 2rem));
  margin-inline: auto;
}

.brand-showcase__header {
  padding-block: var(--pq-space-5);
}

.brand-showcase__main {
  display: grid;
  gap: var(--pq-space-8);
  padding-block: var(--pq-space-12);
}

.brand-showcase__intro {
  max-width: var(--pq-size-content-narrow);
}

.brand-showcase__eyebrow {
  margin: 0;
  color: var(--pq-color-brand);
  font-size: var(--pq-font-size-sm);
  font-weight: var(--pq-font-weight-bold);
  letter-spacing: var(--pq-letter-spacing-wide);
  text-transform: uppercase;
}

h1 {
  margin: var(--pq-space-2) 0 var(--pq-space-4);
  font-size: var(--pq-font-size-4xl);
  line-height: var(--pq-line-height-tight);
  letter-spacing: var(--pq-letter-spacing-tight);
}

.brand-showcase__intro > p:last-child {
  color: var(--pq-color-text-muted);
  font-size: var(--pq-font-size-lg);
  line-height: var(--pq-line-height-body);
}

.brand-showcase__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--pq-space-6);
}

@media (max-width: 48rem) {
  .brand-showcase__grid {
    grid-template-columns: 1fr;
  }
}
</style>
