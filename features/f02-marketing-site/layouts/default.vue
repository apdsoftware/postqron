<script setup lang="ts">
import CookiePreferences from '~/components/CookiePreferences.vue'
import SiteFooter from '~/components/SiteFooter.vue'
import SiteHeader from '~/components/SiteHeader.vue'
import PqSkipLink from '../../f01-brand/components/PqSkipLink.vue'
</script>

<template>
  <div
    class="site-shell"
    data-pq-theme="light"
  >
    <PqSkipLink />
    <SiteHeader />
    <main id="main-content">
      <slot />
    </main>
    <SiteFooter />
    <CookiePreferences />
  </div>
</template>
