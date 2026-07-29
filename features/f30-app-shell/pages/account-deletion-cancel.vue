<script setup lang="ts">
import {
  computed,
  definePageMeta,
  nextTick,
  ref,
  useHead,
  useRoute,
} from '#imports'
import {
  normalizeAppApiError,
} from '../components/core/api.ts'
import {
  appRoot,
  localeFromAppPath,
} from '../components/core/navigation.ts'
import {
  useAccountDeletionCancellationState,
  useAppShellApi,
  useAppShellI18n,
} from '../components/core/use-app-shell.ts'
import type {
  DeletionStatus,
} from '../components/core/contracts.ts'

definePageMeta({ layout: false })

const route = useRoute()
const api = useAppShellApi()
const cancellation = useAccountDeletionCancellationState()
const { t } = useAppShellI18n()
const working = ref(false)
const result = ref<'cancelled' | 'expired' | 'offline' | 'unavailable'>()
const resultHeading = ref<{ focus: () => void }>()

const locale = computed(() => localeFromAppPath(route.fullPath))
const requestId = computed(() => {
  const value = Array.isArray(route.params.requestId)
    ? route.params.requestId[0]
    : route.params.requestId
  const normalized = typeof value === 'string' ? value.trim() : ''
  return normalized !== '' && normalized.length <= 256
    ? normalized
    : undefined
})
const requestState = computed(() =>
  requestId.value
  && cancellation.value?.requestId === requestId.value
    ? cancellation.value
    : undefined)
const status = ref<DeletionStatus | undefined>(requestState.value?.status)
const graceEndsAt = computed(() => requestState.value?.graceEndsAt)
const loginHref = computed(() => appRoot(locale.value))

const statusLabel = computed(() => {
  switch (status.value) {
    case 'grace_period':
      return t('accountDeletionCancel.statusGracePeriod')
    case 'cancelled':
      return t('accountDeletionCancel.statusCancelled')
    case 'deactivating':
    case 'finalizing':
      return t('accountDeletionCancel.statusProcessing')
    case 'completed':
      return t('accountDeletionCancel.statusCompleted')
    case 'deactivation_failed':
    case 'finalization_failed':
      return t('accountDeletionCancel.statusFailed')
    default:
      return t('accountDeletionCancel.statusUnknown')
  }
})

const formattedGraceEnd = computed(() => {
  if (!graceEndsAt.value) {
    return t('accountDeletionCancel.graceUnknown')
  }
  return new Intl.DateTimeFormat(locale.value, {
    dateStyle: 'long',
    timeStyle: 'short',
    timeZone: 'UTC',
  }).format(new Date(graceEndsAt.value))
})

useHead(computed(() => ({
  title: t('documentTitle.accountDeletionCancel'),
})))

async function focusResult() {
  await nextTick()
  resultHeading.value?.focus()
}

async function cancelAccountDeletion() {
  if (!requestId.value || working.value || result.value === 'cancelled') {
    return
  }
  working.value = true
  result.value = undefined
  try {
    await api.cancelAccountDeletion(requestId.value)
    status.value = 'cancelled'
    if (cancellation.value?.requestId === requestId.value) {
      cancellation.value = {
        ...cancellation.value,
        status: 'cancelled',
      }
    }
    result.value = 'cancelled'
  } catch (error) {
    const failure = normalizeAppApiError(error)
    result.value = failure.status === 401
      || failure.status === 403
      || failure.status === 404
      || failure.status === 410
      ? 'expired'
      : failure.retryable
        ? 'offline'
        : 'unavailable'
  } finally {
    working.value = false
    await focusResult()
  }
}
</script>

<template>
  <main class="account-deletion-page">
    <section
      class="account-deletion-card"
      aria-labelledby="account-deletion-title"
    >
      <a
        class="account-deletion-brand"
        :href="loginHref"
      >Postqron</a>
      <p class="app-eyebrow">
        {{ t('accountDeletionCancel.eyebrow') }}
      </p>
      <h1 id="account-deletion-title">
        {{ t('accountDeletionCancel.title') }}
      </h1>
      <p class="account-deletion-card__lead">
        {{ t('accountDeletionCancel.description') }}
      </p>

      <dl class="account-deletion-summary">
        <div>
          <dt>{{ t('accountDeletionCancel.statusLabel') }}</dt>
          <dd>{{ statusLabel }}</dd>
        </div>
        <div>
          <dt>{{ t('accountDeletionCancel.graceEndsLabel') }}</dt>
          <dd>
            <time
              v-if="graceEndsAt"
              :datetime="graceEndsAt"
            >
              {{ formattedGraceEnd }}
            </time>
            <span v-else>{{ formattedGraceEnd }}</span>
          </dd>
        </div>
      </dl>

      <p
        v-if="!requestId"
        ref="resultHeading"
        class="app-inline-alert"
        role="alert"
        tabindex="-1"
      >
        {{ t('accountDeletionCancel.invalidRequest') }}
      </p>
      <section
        v-else-if="result === 'cancelled'"
        class="account-deletion-result"
        role="status"
      >
        <h2
          ref="resultHeading"
          tabindex="-1"
        >
          {{ t('accountDeletionCancel.successTitle') }}
        </h2>
        <p>{{ t('accountDeletionCancel.successDescription') }}</p>
        <a
          class="pq-button"
          :href="loginHref"
        >
          {{ t('accountDeletionCancel.login') }}
        </a>
      </section>
      <p
        v-else-if="result"
        ref="resultHeading"
        class="app-inline-alert"
        role="alert"
        tabindex="-1"
      >
        {{
          result === 'expired'
            ? t('accountDeletionCancel.errorExpired')
            : result === 'offline'
              ? t('accountDeletionCancel.errorOffline')
              : t('accountDeletionCancel.errorUnavailable')
        }}
      </p>

      <button
        v-if="result !== 'cancelled'"
        class="pq-button"
        type="button"
        :aria-busy="working"
        :disabled="!requestId || working"
        @click="cancelAccountDeletion"
      >
        {{
          working
            ? t('accountDeletionCancel.cancelling')
            : result === 'offline'
              ? t('accountDeletionCancel.retry')
              : t('accountDeletionCancel.action')
        }}
      </button>

      <p class="account-deletion-security-note">
        {{ t('accountDeletionCancel.securityNote') }}
      </p>
    </section>
  </main>
</template>
