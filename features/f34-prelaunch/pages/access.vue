<script setup lang="ts">
import {
  computed,
  definePageMeta,
  useRoute,
  useRuntimeConfig,
  useSeoMeta,
} from '#imports'
import { localizeUrl } from '../../f36-i18n/src/routing.ts'
import { usePrelaunch } from '../runtime.ts'

definePageMeta({ layout: 'prelaunch' })

const prelaunch = usePrelaunch()
const config = useRuntimeConfig()
const route = useRoute()
const result = computed(() => String(route.query.result || ''))
const status = computed<'idle' | 'success' | 'error' | 'rate'>(() => {
  if (result.value === 'success') {
    return 'success'
  }
  if (result.value === 'rate') {
    return 'rate'
  }
  if (result.value === 'error' || result.value === 'invalid') {
    return 'error'
  }
  return 'idle'
})
const title = computed(() => prelaunch.translate('access.metaTitle'))
const description = computed(() =>
  prelaunch.translate('access.metaDescription'))
const backUrl = computed(() =>
  localizeUrl(prelaunch.locale.value, '/prelaunch'))
const apiBase = computed(() =>
  String(config.public.apiBase || '').replace(/\/+$/u, ''))
const formAction = computed(() =>
  `${apiBase.value}/api/v1/prelaunch/access-requests`)

useSeoMeta({
  title,
  description,
  ogTitle: title,
  ogDescription: description,
  robots: 'noindex, nofollow',
})
</script>

<template>
  <div class="prelaunch-access">
    <div>
      <p class="eyebrow">
        {{ prelaunch.translate('access.eyebrow') }}
      </p>
      <h1>{{ prelaunch.translate('access.title') }}</h1>
      <p class="prelaunch-access__lead">
        {{ prelaunch.translate('access.description') }}
      </p>
    </div>

    <form
      class="prelaunch-access__form"
      :action="formAction"
      method="post"
      enctype="application/x-www-form-urlencoded"
    >
      <div
        v-if="status === 'success'"
        class="prelaunch-access__notice"
        role="status"
        tabindex="-1"
      >
        <strong>{{ prelaunch.translate('access.successTitle') }}</strong>
        <p>{{ prelaunch.translate('access.success') }}</p>
      </div>

      <div
        v-if="status === 'error' || status === 'rate'"
        class="prelaunch-access__error"
        role="alert"
      >
        {{ status === 'rate'
          ? prelaunch.translate('access.rateLimited')
          : prelaunch.translate('access.error') }}
      </div>

      <label class="pq-field">
        <span class="pq-field__label">
          {{ prelaunch.translate('access.emailLabel') }}
        </span>
        <input
          class="pq-field__input"
          name="email"
          type="email"
          inputmode="email"
          autocomplete="email"
          maxlength="254"
          required
          aria-describedby="prelaunch-email-help"
        >
        <span
          id="prelaunch-email-help"
          class="pq-field__help"
        >
          {{ prelaunch.translate('access.emailHelp') }}
        </span>
      </label>

      <label class="prelaunch-access__consent">
        <input
          name="access_consent"
          type="checkbox"
          value="true"
          required
        >
        <span>{{ prelaunch.translate('access.consent') }}</span>
      </label>

      <input
        type="hidden"
        name="locale"
        :value="prelaunch.locale.value"
      >
      <input
        type="hidden"
        name="marketing_consent"
        value="false"
      >
      <input
        type="hidden"
        name="consent_policy_version"
        value="prelaunch-access-v1"
      >
      <input
        type="hidden"
        name="return_path"
        :value="localizeUrl(prelaunch.locale.value, '/prelaunch/access')"
      >

      <button
        class="pq-button"
        type="submit"
      >
        {{ prelaunch.translate('access.submit') }}
      </button>
    </form>

    <NuxtLink
      class="prelaunch-access__back"
      :to="backUrl"
    >
      ← {{ prelaunch.translate('access.back') }}
    </NuxtLink>
  </div>
</template>

<style scoped>
.prelaunch-access {
  display: grid;
  width: min(calc(100% - 2rem), 46rem);
  gap: 2rem;
  margin-inline: auto;
  padding: clamp(3rem, 8vw, 7rem) 0 clamp(4rem, 10vw, 8rem);
}

.prelaunch-access h1 {
  max-width: 15ch;
  margin: 0;
  font-size: clamp(2.25rem, 7vw, 4rem);
  line-height: var(--pq-line-height-tight);
  letter-spacing: var(--pq-letter-spacing-tight);
}

.prelaunch-access__lead {
  max-width: 58ch;
  color: var(--pq-color-text-muted);
  font-size: var(--pq-font-size-lg);
  line-height: var(--pq-line-height-body);
}

.prelaunch-access__form {
  display: grid;
  gap: 1.5rem;
  border: 1px solid var(--pq-color-border);
  border-radius: var(--pq-radius-xl);
  padding: clamp(1.25rem, 5vw, 2.5rem);
  background: #fff;
  box-shadow: var(--pq-shadow-md);
}

.prelaunch-access__consent {
  display: grid;
  grid-template-columns: 1.25rem minmax(0, 1fr);
  gap: 0.75rem;
  align-items: start;
  color: var(--pq-color-text-muted);
  line-height: 1.55;
}

.prelaunch-access__consent input {
  width: 1.15rem;
  height: 1.15rem;
  margin-top: 0.2rem;
  accent-color: var(--pq-color-brand);
}

.prelaunch-access__notice,
.prelaunch-access__error {
  border-radius: var(--pq-radius-md);
  padding: 1rem;
  line-height: var(--pq-line-height-body);
}

.prelaunch-access__notice {
  border: 1px solid var(--pq-color-success);
  background: var(--pq-color-success-surface);
}

.prelaunch-access__notice p {
  margin: 0.35rem 0 0;
}

.prelaunch-access__error {
  border: 1px solid var(--pq-color-danger);
  color: var(--pq-color-danger);
  background: var(--pq-color-danger-surface);
}

.prelaunch-access__back {
  justify-self: start;
  color: var(--pq-color-brand);
  font-weight: var(--pq-font-weight-semibold);
}

.prelaunch-access__back:focus-visible,
.prelaunch-access__consent input:focus-visible {
  outline: var(--pq-border-focus) solid var(--pq-color-focus);
  outline-offset: 3px;
}

@media (max-width: 30rem) {
  .prelaunch-access__form .pq-button {
    width: 100%;
  }
}
</style>
