<script setup lang="ts">
import { useRuntimeConfig } from '#imports'

const config = useRuntimeConfig()

function openCookiePreferences() {
  if (import.meta.client) {
    globalThis.window.dispatchEvent(
      new globalThis.CustomEvent('postqron:open-cookie-preferences'),
    )
  }
}
</script>

<template>
  <footer class="site-footer">
    <div class="content-wrap site-footer__grid">
      <div>
        <strong>Postqron</strong>
        <p>Programma e gestisci i tuoi contenuti social da un unico spazio.</p>
      </div>
      <nav aria-label="Informazioni sul prodotto">
        <strong>Prodotto</strong>
        <NuxtLink to="/funzionalita">
          Funzionalità
        </NuxtLink>
        <NuxtLink to="/prezzi">
          Prezzi
        </NuxtLink>
        <NuxtLink to="/faq">
          FAQ
        </NuxtLink>
      </nav>
      <nav aria-label="Documenti legali">
        <strong>Legale</strong>
        <NuxtLink to="/legal/termini">
          Termini e condizioni
        </NuxtLink>
        <NuxtLink to="/legal/privacy">
          Privacy Policy
        </NuxtLink>
        <NuxtLink to="/legal/cookie">
          Cookie Policy
        </NuxtLink>
        <button
          type="button"
          @click="openCookiePreferences"
        >
          Gestisci cookie
        </button>
      </nav>
    </div>
    <div class="content-wrap site-footer__bottom">
      <span>© {{ new Date().getFullYear() }} Postqron</span>
      <span>
        Sviluppato da
        <a
          :href="config.public.apdSoftwareUrl"
          rel="noopener noreferrer"
        >APDSoftware</a>
      </span>
    </div>
  </footer>
</template>
