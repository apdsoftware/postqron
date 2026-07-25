<script setup lang="ts">
import { computed } from '#imports'
import PqSkipLink from '../../f01-brand/components/PqSkipLink.vue'
import { localizeUrl } from '../../f36-i18n/src/routing.ts'
import { usePrelaunch } from '../runtime.ts'

const prelaunch = usePrelaunch()
const link = (path: string) => computed(() =>
  localizeUrl(prelaunch.locale.value, path))
</script>

<template>
  <div
    class="prelaunch-shell"
    data-pq-theme="light"
  >
    <PqSkipLink />
    <header class="prelaunch-header">
      <NuxtLink
        :to="link('/prelaunch').value"
        :aria-label="prelaunch.translate('nav.home')"
      >
        <img
          src="/brand/logo-primary.svg"
          width="154"
          height="42"
          alt="Postqron"
        >
      </NuxtLink>
      <div
        class="prelaunch-header__language"
        :aria-label="prelaunch.translate('nav.language')"
      >
        <PostqronLanguageSwitcher />
      </div>
    </header>

    <main
      id="main-content"
      tabindex="-1"
    >
      <slot />
    </main>

    <footer class="prelaunch-footer">
      <p>© {{ new Date().getUTCFullYear() }} Postqron</p>
      <nav aria-label="Legal">
        <NuxtLink :to="link('/legal/privacy').value">
          {{ prelaunch.translate('footer.privacy') }}
        </NuxtLink>
        <NuxtLink :to="link('/legal/cookies').value">
          {{ prelaunch.translate('footer.cookies') }}
        </NuxtLink>
        <NuxtLink :to="link('/legal/terms').value">
          {{ prelaunch.translate('footer.terms') }}
        </NuxtLink>
        <a
          href="mailto:help@postqron.com"
          :aria-label="prelaunch.translate('footer.supportLabel')"
        >
          {{ prelaunch.translate('footer.support') }}
        </a>
      </nav>
    </footer>
  </div>
</template>

<style>
.prelaunch-shell {
  min-height: 100vh;
  color: var(--pq-color-text);
  background:
    radial-gradient(circle at 84% 4%, #ffc9b366 0, transparent 28rem),
    linear-gradient(180deg, #f4f8f5 0%, #fff 65%);
  font-family: var(--pq-font-sans);
}

.prelaunch-header,
.prelaunch-footer {
  width: min(calc(100% - 2rem), 72rem);
  margin-inline: auto;
}

.prelaunch-header {
  display: flex;
  min-height: 5.5rem;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
}

.prelaunch-header img {
  display: block;
  width: 9.625rem;
  height: auto;
}

.prelaunch-header__language {
  min-width: 8rem;
}

.prelaunch-footer {
  display: flex;
  justify-content: space-between;
  gap: 1.5rem;
  border-top: 1px solid var(--pq-color-border);
  padding-block: 2rem 3rem;
  color: var(--pq-color-text-muted);
  font-size: var(--pq-font-size-sm);
}

.prelaunch-footer p {
  margin: 0;
}

.prelaunch-footer nav {
  display: flex;
  flex-wrap: wrap;
  gap: 1rem 1.5rem;
}

.prelaunch-footer a {
  color: inherit;
}

.prelaunch-footer a:focus-visible {
  outline: var(--pq-border-focus) solid var(--pq-color-focus);
  outline-offset: 3px;
}

@media (max-width: 40rem) {
  .prelaunch-header {
    min-height: 4.75rem;
  }

  .prelaunch-footer {
    flex-direction: column;
  }
}
</style>
