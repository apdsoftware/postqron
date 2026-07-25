<script setup lang="ts">
import { useRuntimeConfig } from '#imports'
import { useSupportContact } from '../../f33-support-contact/runtime'

const config = useRuntimeConfig()
const support = useSupportContact()
const currentYear = new Date().getFullYear()

function openCookiePreferences() {
  if (import.meta.client) {
    globalThis.window.dispatchEvent(
      new globalThis.CustomEvent('postqron:open-cookie-preferences'),
    )
  }
}
</script>

<template>
  <!-- The Italian default remains “Sviluppato da”; visible copy comes from the active catalog. -->
  <footer class="site-footer">
    <div class="content-wrap site-footer__grid">
      <div>
        <strong>Postqron</strong>
        <p>{{ support.translate('footer.description') }}</p>
        <strong>{{ support.translate('footer.supportHeading') }}</strong>
        <p>
          <a
            :href="support.mailto()"
            :aria-label="support.translate('footer.supportLinkLabel', {
              email: support.config.value.email,
            })"
          >{{ support.config.value.email }}</a>
        </p>
      </div>
      <nav :aria-label="support.translate('footer.productNavLabel')">
        <strong>{{ support.translate('footer.productHeading') }}</strong>
        <NuxtLink :to="support.localize('/funzionalita')">
          {{ support.translate('footer.features') }}
        </NuxtLink>
        <NuxtLink :to="support.localize('/prezzi')">
          {{ support.translate('footer.pricing') }}
        </NuxtLink>
        <NuxtLink :to="support.localize('/faq')">
          {{ support.translate('footer.faq') }}
        </NuxtLink>
        <NuxtLink :to="support.localize('/contatti')">
          {{ support.translate('footer.contact') }}
        </NuxtLink>
      </nav>
      <nav :aria-label="support.translate('footer.legalNavLabel')">
        <strong>{{ support.translate('footer.legalHeading') }}</strong>
        <NuxtLink :to="support.localize('/legal/termini')">
          {{ support.translate('footer.terms') }}
        </NuxtLink>
        <NuxtLink :to="support.localize('/legal/privacy')">
          {{ support.translate('footer.privacy') }}
        </NuxtLink>
        <NuxtLink :to="support.localize('/legal/cookie')">
          {{ support.translate('footer.cookies') }}
        </NuxtLink>
        <button
          type="button"
          @click="openCookiePreferences"
        >
          {{ support.translate('footer.manageCookies') }}
        </button>
      </nav>
    </div>
    <div class="content-wrap site-footer__bottom">
      <span>© {{ currentYear }} Postqron</span>
      <span>
        {{ support.translate('footer.developedBy') }}
        <a
          :href="config.public.apdSoftwareUrl"
          rel="noopener noreferrer"
        >APDSoftware</a>
      </span>
    </div>
  </footer>
</template>
