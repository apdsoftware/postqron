<script setup lang="ts">
import {
  computed,
  definePageMeta,
  ref,
  useAsyncData,
  useHead,
  useRoute,
} from '#imports'
import { localeFromAppPath } from '../components/core/navigation.ts'
import {
  appStateKindFromError,
  useAppSessionState,
  useAppShellApi,
  useAppShellI18n,
  useSocialConnectionsApi,
} from '../components/core/use-app-shell.ts'
import { formatDateTime } from '../components/core/preferences.ts'
import { normalizeSocialApiError } from '../components/core/social-api.ts'
import type {
  SocialBootstrap,
  SocialConnection,
  SocialProvider,
  SocialSelection,
} from '../components/core/social-connections.ts'
import type { AppShellMessageKey } from '../components/core/catalogs.ts'

definePageMeta({ layout: 'app-shell' })

const route = useRoute()
const accountApi = useAppShellApi()
const social = useSocialConnectionsApi()
const session = useAppSessionState()
const { t, locale: uiLocale } = useAppShellI18n()

const bootstrap = ref<SocialBootstrap>()
const connections = ref<SocialConnection[]>()
const selection = ref<SocialSelection>()
const pageState = ref<'access-denied' | 'offline' | 'unavailable'>()
const notice = ref<{
  tone: 'error' | 'success'
  key: AppShellMessageKey
  params?: Readonly<Record<string, string>>
}>()
const connecting = ref<SocialProvider>()
const reconnecting = ref<string>()
const revoking = ref<string>()
const selecting = ref<string>()
const callbackProcessed = ref(false)

const workspaceId = computed(() => session.value?.current_workspace?.id ?? '')

useHead(computed(() => ({
  title: t('documentTitle.socialChannels'),
})))

function queryString(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

// Every failure maps to a stable, retry-aware, fail-closed message. A code the
// runtime has not declared collapses to the generic error, never to success.
function socialErrorKey(error: unknown): AppShellMessageKey {
  switch (normalizeSocialApiError(error).kind) {
    case 'quota-exceeded':
      return 'social.errorQuota'
    case 'quota-unavailable':
      return 'social.errorQuotaUnavailable'
    case 'provider-unavailable':
      return 'social.errorProviderUnavailable'
    case 'already-connected':
      return 'social.errorAlreadyConnected'
    case 'flow-expired':
      return 'social.errorFlowExpired'
    case 'invalid-state':
      return 'social.errorState'
    case 'provider-denied':
      return 'social.errorProviderDenied'
    case 'no-resources':
      return 'social.errorNoResources'
    case 'access-denied':
      return 'social.errorForbidden'
    case 'session':
      return 'social.errorSession'
    case 'not-found':
      return 'social.errorNotFound'
    default:
      return 'social.error'
  }
}

function socialPageState(error: unknown): 'access-denied' | 'offline' | 'unavailable' {
  const kind = normalizeSocialApiError(error).kind
  if (kind === 'offline') {
    return 'offline'
  }
  if (kind === 'access-denied' || kind === 'session') {
    return 'access-denied'
  }
  return appStateKindFromError(error)
}

async function processCallback() {
  const state = queryString(route.query.state)
  const code = queryString(route.query.code)
  const providerError = queryString(route.query.error)
  if (!state && !code && !providerError) {
    return
  }
  try {
    selection.value = await social.completeAuthorization({
      state,
      code,
      error: providerError,
    })
    notice.value = undefined
  } catch (error) {
    selection.value = undefined
    notice.value = { tone: 'error', key: socialErrorKey(error) }
  }
}

const { pending, refresh } = useAsyncData('postqron-social-channels', async () => {
  try {
    if (!session.value) {
      session.value = await accountApi.session()
    }
    bootstrap.value = await social.bootstrap(workspaceId.value)
    connections.value = await social.list(workspaceId.value)
    pageState.value = undefined
    if (!callbackProcessed.value) {
      callbackProcessed.value = true
      await processCallback()
    }
    return { loaded: true }
  } catch (error) {
    connections.value = undefined
    pageState.value = socialPageState(error)
    return undefined
  }
}, { server: false })

function reasonKey(connection: SocialConnection): AppShellMessageKey {
  return connection.reconnect_reason
    ? `social.reason.${connection.reconnect_reason}`
    : 'social.reason.generic'
}

function statusBadgeClass(status: SocialConnection['status']): string {
  if (status === 'connected') {
    return 'app-badge app-badge--success'
  }
  if (status === 'reconnect_required') {
    return 'app-badge app-badge--warning'
  }
  return 'app-badge'
}

async function connect(provider: SocialProvider) {
  connecting.value = provider
  notice.value = undefined
  try {
    const authorization = await social.begin(workspaceId.value, provider)
    if (import.meta.client) {
      globalThis.location.assign(authorization.authorization_url)
    }
  } catch (error) {
    notice.value = { tone: 'error', key: socialErrorKey(error) }
  } finally {
    connecting.value = undefined
  }
}

async function reconnect(connection: SocialConnection) {
  reconnecting.value = connection.id
  notice.value = undefined
  try {
    const authorization = await social.reconnect(workspaceId.value, connection.id)
    if (import.meta.client) {
      globalThis.location.assign(authorization.authorization_url)
    }
  } catch (error) {
    notice.value = { tone: 'error', key: socialErrorKey(error) }
  } finally {
    reconnecting.value = undefined
  }
}

async function selectResource(remoteId: string) {
  const active = selection.value
  if (!active) {
    return
  }
  selecting.value = remoteId
  notice.value = undefined
  try {
    const connection = await social.selectResource(workspaceId.value, {
      selectionId: active.selection_id,
      remoteId,
    })
    connections.value = await social.list(workspaceId.value)
    selection.value = undefined
    notice.value = {
      tone: 'success',
      key: 'social.connectedNotice',
      params: { name: connection.display_name },
    }
  } catch (error) {
    notice.value = { tone: 'error', key: socialErrorKey(error) }
  } finally {
    selecting.value = undefined
  }
}

async function disconnect(connection: SocialConnection) {
  revoking.value = connection.id
  notice.value = undefined
  try {
    await social.revoke(workspaceId.value, connection.id)
    connections.value = await social.list(workspaceId.value)
    notice.value = { tone: 'success', key: 'social.disconnected' }
  } catch (error) {
    notice.value = { tone: 'error', key: socialErrorKey(error) }
  } finally {
    revoking.value = undefined
  }
}

function cancelSelection() {
  selection.value = undefined
}

async function retry() {
  await refresh()
}

// Only expose what belongs to the current locale's app routes.
const locale = computed(() => localeFromAppPath(route.fullPath))
</script>

<template>
  <AppState
    v-if="pending && connections === undefined"
    kind="loading"
  />
  <AppState
    v-else-if="pageState"
    :kind="pageState"
    action
    @retry="retry"
  />
  <section
    v-else
    class="app-page"
    :data-locale="locale"
  >
    <p class="app-eyebrow">
      {{ t('social.eyebrow') }}
    </p>
    <h1>{{ t('social.title') }}</h1>
    <p class="app-page__lead">
      {{ t('social.description') }}
    </p>
    <p class="app-inline-note">
      {{ t('social.scopeNote') }}
    </p>
    <p class="app-inline-note">
      {{ t('social.requirementsNote') }}
    </p>

    <p
      v-if="notice"
      class="app-inline-alert"
      :data-success="notice.tone === 'success'"
      :role="notice.tone === 'success' ? 'status' : 'alert'"
    >
      {{ t(notice.key, notice.params ?? {}) }}
    </p>

    <div class="app-page__stack">
      <article
        v-if="selection"
        class="app-card app-card--accent"
      >
        <div class="app-card__header">
          <span class="app-card__eyebrow">{{ t('social.selectionEyebrow') }}</span>
          <h2>{{ t('social.selectionTitle') }}</h2>
        </div>
        <p class="app-page__lead">
          {{ t('social.selectionLead') }}
        </p>
        <ul class="app-provider-list">
          <li
            v-for="resource in selection.resources"
            :key="resource.remote_id"
          >
            <div class="app-provider-list__meta">
              <strong>{{ resource.display_name }}</strong>
              <span>
                {{ t(`social.provider.${selection.provider}`) }}
                · {{ t(`social.accountType.${resource.account_type}`) }}
                <template v-if="resource.handle">· @{{ resource.handle }}</template>
              </span>
            </div>
            <button
              class="pq-button"
              type="button"
              :disabled="selecting === resource.remote_id"
              @click="selectResource(resource.remote_id)"
            >
              {{ selecting === resource.remote_id ? t('social.selecting') : t('social.select') }}
            </button>
          </li>
        </ul>
        <button
          class="pq-button pq-button--secondary"
          type="button"
          @click="cancelSelection"
        >
          {{ t('social.cancelSelection') }}
        </button>
      </article>

      <article class="app-card">
        <div class="app-card__header">
          <span class="app-card__eyebrow">{{ t('social.connectedEyebrow') }}</span>
          <h2>{{ t('social.connectedTitle') }}</h2>
        </div>
        <AppState
          v-if="(connections?.length ?? 0) === 0"
          kind="empty"
        />
        <ul
          v-else
          class="app-provider-list"
        >
          <li
            v-for="connection in connections"
            :key="connection.id"
          >
            <div class="app-provider-list__meta">
              <strong>{{ connection.display_name }}</strong>
              <span>
                {{ t(`social.provider.${connection.provider}`) }}
                · {{ t(`social.accountType.${connection.account_type}`) }}
                <template v-if="connection.handle">· @{{ connection.handle }}</template>
              </span>
              <span>
                {{
                  connection.last_verified_at
                    ? t('social.lastVerified', {
                      date: formatDateTime(connection.last_verified_at, uiLocale.value),
                    })
                    : t('social.neverVerified')
                }}
              </span>
              <span
                v-if="connection.status === 'reconnect_required'"
                class="app-inline-note"
              >
                {{ t(reasonKey(connection)) }}
              </span>
            </div>
            <span :class="statusBadgeClass(connection.status)">
              {{ t(`social.status.${connection.status}`) }}
            </span>
            <div class="app-action-stack">
              <button
                v-if="connection.status === 'reconnect_required'"
                class="pq-button"
                type="button"
                :disabled="reconnecting === connection.id"
                @click="reconnect(connection)"
              >
                {{ reconnecting === connection.id ? t('social.reconnecting') : t('social.reconnect') }}
              </button>
              <button
                class="pq-button pq-button--secondary"
                type="button"
                :disabled="revoking === connection.id"
                @click="disconnect(connection)"
              >
                {{ revoking === connection.id ? t('social.disconnecting') : t('social.disconnect') }}
              </button>
            </div>
          </li>
        </ul>
      </article>

      <article class="app-card">
        <div class="app-card__header">
          <span class="app-card__eyebrow">{{ t('social.availabilityEyebrow') }}</span>
          <h2>{{ t('social.availabilityTitle') }}</h2>
        </div>
        <ul class="app-provider-list">
          <li
            v-for="provider in bootstrap?.providers ?? []"
            :key="provider.provider"
          >
            <div class="app-provider-list__meta">
              <strong>{{ t(`social.provider.${provider.provider}`) }}</strong>
              <span>
                {{
                  provider.status === 'available'
                    ? t('social.connectHint')
                    : t('social.providerUnavailableHint')
                }}
              </span>
            </div>
            <button
              v-if="provider.status === 'available'"
              class="pq-button"
              type="button"
              :disabled="connecting === provider.provider"
              @click="connect(provider.provider)"
            >
              {{ connecting === provider.provider ? t('social.connecting') : t('social.connect') }}
            </button>
            <span
              v-else
              class="app-badge app-badge--warning"
            >
              {{ t('social.providerUnavailable') }}
            </span>
          </li>
        </ul>
      </article>
    </div>
  </section>
</template>
