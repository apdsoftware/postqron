<script setup lang="ts">
import {
  computed,
  definePageMeta,
  navigateTo,
  nextTick,
  ref,
  useAsyncData,
  useHead,
  useRoute,
} from '#imports'
import {
  appRoute,
  localeFromAppPath,
} from '../components/core/navigation.ts'
import { parseSocialCallbackDocument } from '../components/core/social-callback.ts'
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
  SocialDiscoveryInput,
  SocialDiscoveryInputKind,
  SocialProviderCatalogEntry,
  SocialProvider,
  SocialSelection,
} from '../components/core/social-connections.ts'
import { publishingModesForConnection } from '../components/core/social-connections.ts'
import type { AppShellMessageKey } from '../components/core/catalogs.ts'

definePageMeta({ layout: 'app-shell' })

const route = useRoute()
const accountApi = useAppShellApi()
const social = useSocialConnectionsApi()
const session = useAppSessionState()
const { t, locale: uiLocale } = useAppShellI18n()
const locale = computed(() => localeFromAppPath(route.fullPath))

const bootstrap = ref<SocialBootstrap>()
const connections = ref<SocialConnection[]>()
const selection = ref<SocialSelection>()
const workspace = ref<Awaited<ReturnType<typeof accountApi.currentWorkspace>>>()
const pageState = ref<'access-denied' | 'offline' | 'unavailable'>()
const notice = ref<{
  tone: 'error' | 'success'
  key: AppShellMessageKey
  params?: Readonly<Record<string, string>>
}>()
const discoveryKinds = ref<Partial<Record<SocialProvider, SocialDiscoveryInputKind>>>({})
const discoveryValues = ref<Partial<Record<SocialProvider, string>>>({})
const connecting = ref<SocialProvider>()
const reconnecting = ref<string>()
const revoking = ref<string>()
const selecting = ref<string>()
const callbackProcessed = ref(false)
const catalogHeading = ref<{
  focus(): void
  scrollIntoView(options: { behavior: 'smooth', block: 'start' }): void
}>()

const workspaceId = computed(() => session.value?.current_workspace?.id ?? '')
const canManageChannels = computed(() =>
  workspace.value?.role === 'owner' && workspace.value?.status === 'active')

type CallbackPopupHandle = {
  closed: boolean
  close(): void
  location: {
    assign(url: string): void
    origin: string
    pathname: string
  }
  document: {
    body: {
      textContent: string | null
    } | null
  }
}

useHead(computed(() => ({
  title: t('documentTitle.socialChannels'),
})))

function queryString(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

// Every failure maps to a stable, retry-aware, fail-closed message. A code the
// runtime has not declared collapses to the generic error, never to success.
function socialErrorKey(error: unknown): AppShellMessageKey {
  const failure = normalizeSocialApiError(error)
  if (failure.code === 'callback_handoff_unavailable') {
    return 'social.errorCallbackHandoff'
  }
  switch (failure.kind) {
    case 'quota-exceeded':
      return 'social.errorQuota'
    case 'quota-unavailable':
      return 'social.errorQuotaUnavailable'
    case 'provider-unavailable':
    case 'provider-not-configured':
      return 'social.errorProviderUnavailable'
    case 'provider-review-required':
      return 'social.errorProviderReview'
    case 'provider-audit-required':
      return 'social.errorProviderAudit'
    case 'provider-temporary':
      return 'social.errorProviderTemporary'
    case 'provider-access-denied':
      return 'social.errorProviderAccessDenied'
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
  const issuer = queryString(route.query.iss)
  const providerError = queryString(route.query.error)
  if (!state && !code && !issuer && !providerError) {
    return
  }
  try {
    selection.value = await social.completeAuthorization({
      state,
      code,
      error: providerError,
      iss: issuer,
    })
    notice.value = undefined
  } catch (error) {
    selection.value = undefined
    notice.value = { tone: 'error', key: socialErrorKey(error) }
  } finally {
    await navigateTo(appRoute(locale.value, 'social-channels'), { replace: true })
  }
}

const { pending, refresh } = useAsyncData('postqron-social-channels', async () => {
  try {
    if (!session.value) {
      session.value = await accountApi.session()
    }
    const [currentWorkspace, currentBootstrap, currentConnections] = await Promise.all([
      accountApi.currentWorkspace(),
      social.bootstrap(workspaceId.value),
      social.list(workspaceId.value),
    ])
    workspace.value = currentWorkspace
    bootstrap.value = currentBootstrap
    connections.value = currentConnections
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

function ensureManagePermission(): boolean {
  if (canManageChannels.value) {
    return true
  }
  notice.value = { tone: 'error', key: 'social.errorForbidden' }
  return false
}

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

function catalogState(
  provider: SocialProviderCatalogEntry,
): 'available' | 'configuring' | 'unavailable' {
  if (provider.status === 'available' && provider.configuration_state === 'ready') {
    return 'available'
  }
  if (provider.configuration_state === 'review_required'
    || provider.configuration_state === 'audit_required') {
    return 'configuring'
  }
  return 'unavailable'
}

function connectionPublishingModes(connection: SocialConnection): string {
  const activeBootstrap = bootstrap.value
  if (!activeBootstrap) {
    return t('social.publishingMode.unknown')
  }
  return publishingModesForConnection(activeBootstrap, connection)
    .map(mode => t(`social.publishingMode.${mode}`))
    .join(', ') || t('social.publishingMode.unknown')
}

function configurationMessage(provider: SocialProviderCatalogEntry): string {
  if ((provider.provider === 'mastodon' || provider.provider === 'bluesky')
    && catalogState(provider) !== 'available') {
    return t('social.configuration.decentralized_blocked')
  }
  return t(`social.configuration.${provider.configuration_state}`)
}

function popupFeatures(): string {
  return [
    'popup=yes',
    'width=560',
    'height=720',
    'resizable=yes',
    'scrollbars=yes',
  ].join(',')
}

function callbackPopup(): CallbackPopupHandle | null {
  if (!import.meta.client) {
    return null
  }
  const popup = globalThis.open('', 'postqron-social-authorization', popupFeatures())
  return popup as CallbackPopupHandle | null
}

function discoveryInput(
  provider: SocialProviderCatalogEntry,
): SocialDiscoveryInput | undefined {
  if (!provider.capabilities.dynamic_discovery) {
    return undefined
  }
  const kind = discoveryKinds.value[provider.provider]
  const value = discoveryValues.value[provider.provider]?.trim()
  if (!kind || !value) {
    notice.value = { tone: 'error', key: 'social.errorDiscoveryRequired' }
    return undefined
  }
  return { kind, value }
}

function isCrossOriginSecurityError(error: unknown): error is { name: string } {
  return typeof error === 'object'
    && error !== null
    && 'name' in error
    && error.name === 'SecurityError'
}

async function waitForSelection(windowHandle: CallbackPopupHandle): Promise<SocialSelection> {
  return await new Promise((resolve, reject) => {
    const deadline = Date.now() + 120_000
    const timer = globalThis.setInterval(() => {
      if (windowHandle.closed) {
        globalThis.clearInterval(timer)
        reject(normalizeSocialApiError({
          data: {
            code: 'popup_closed',
            message: 'The social authorization window was closed.',
            retryable: false,
          },
        }))
        return
      }
      if (Date.now() > deadline) {
        globalThis.clearInterval(timer)
        windowHandle.close()
        reject(normalizeSocialApiError({
          data: {
            code: 'callback_handoff_unavailable',
            message: 'The callback response could not be handed back to the UI securely.',
            retryable: false,
          },
        }))
        return
      }
      try {
        if (windowHandle.location.origin !== globalThis.location.origin
          || windowHandle.location.pathname !== '/api/v1/social-authorizations/callback') {
          return
        }
        const selectionDocument = windowHandle.document.body?.textContent ?? ''
        const parsed = parseSocialCallbackDocument(selectionDocument)
        globalThis.clearInterval(timer)
        windowHandle.close()
        resolve(parsed)
      } catch (error) {
        if (isCrossOriginSecurityError(error)) {
          return
        }
        globalThis.clearInterval(timer)
        windowHandle.close()
        reject(error)
      }
    }, 250)
  })
}

function openCatalog() {
  catalogHeading.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  void nextTick(() => catalogHeading.value?.focus())
}

async function connect(provider: SocialProviderCatalogEntry) {
  if (!ensureManagePermission()) {
    return
  }
  const popup = callbackPopup()
  if (!popup) {
    notice.value = { tone: 'error', key: 'social.errorPopupBlocked' }
    return
  }
  connecting.value = provider.provider
  notice.value = undefined
  try {
    const discovery = discoveryInput(provider)
    if (provider.capabilities.dynamic_discovery && !discovery) {
      popup.close()
      return
    }
    const authorization = await social.begin(
      workspaceId.value,
      provider.provider,
      discovery,
    )
    popup.location.assign(authorization.authorization_url)
    selection.value = await waitForSelection(popup)
  } catch (error) {
    popup.close()
    notice.value = { tone: 'error', key: socialErrorKey(error) }
  } finally {
    connecting.value = undefined
  }
}

async function reconnect(connection: SocialConnection) {
  if (!ensureManagePermission()) {
    return
  }
  const popup = callbackPopup()
  if (!popup) {
    notice.value = { tone: 'error', key: 'social.errorPopupBlocked' }
    return
  }
  reconnecting.value = connection.id
  notice.value = undefined
  try {
    const authorization = await social.reconnect(workspaceId.value, connection.id)
    popup.location.assign(authorization.authorization_url)
    selection.value = await waitForSelection(popup)
  } catch (error) {
    popup.close()
    notice.value = { tone: 'error', key: socialErrorKey(error) }
  } finally {
    reconnecting.value = undefined
  }
}

async function selectResource(remoteId: string) {
  if (!ensureManagePermission()) {
    return
  }
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
  if (!ensureManagePermission()) {
    return
  }
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
    <div class="app-page__title-row">
      <div>
        <h1>{{ t('social.title') }}</h1>
        <p class="app-page__lead">
          {{ t('social.description') }}
        </p>
      </div>
      <button
        class="pq-button"
        type="button"
        @click="openCatalog"
      >
        <span aria-hidden="true">＋</span>
        {{ t('social.addChannel') }}
      </button>
    </div>
    <p class="app-inline-note">
      {{ t('social.scopeNote') }}
    </p>
    <p class="app-inline-note">
      {{ t('social.requirementsNote') }}
    </p>
    <p
      v-if="!canManageChannels"
      class="app-inline-note"
    >
      {{ t('social.manageRestricted') }}
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
              :disabled="!canManageChannels || selecting === resource.remote_id"
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
                {{ t('social.publishingMode') }}:
                {{ connectionPublishingModes(connection) }}
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
                :disabled="!canManageChannels || reconnecting === connection.id"
                @click="reconnect(connection)"
              >
                {{ reconnecting === connection.id ? t('social.reconnecting') : t('social.reconnect') }}
              </button>
              <button
                class="pq-button pq-button--secondary"
                type="button"
                :disabled="!canManageChannels || revoking === connection.id"
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
          <h2
            id="provider-catalog"
            ref="catalogHeading"
            tabindex="-1"
          >
            {{ t('social.availabilityTitle') }}
          </h2>
        </div>
        <p>{{ t('social.catalogDescription') }}</p>
        <ul class="app-provider-catalog">
          <li
            v-for="provider in bootstrap?.catalog ?? []"
            :key="provider.provider"
          >
            <div class="app-provider-list__meta">
              <strong>{{ t(`social.provider.${provider.provider}`) }}</strong>
              <span>
                {{ provider.resources.map(resource => t(`social.resource.${resource.resource_type}`)).join(' · ') }}
              </span>
              <span>
                {{
                  provider.resources
                    .flatMap(resource => resource.publishing_modes)
                    .filter((mode, index, modes) => modes.indexOf(mode) === index)
                    .map(mode => t(`social.publishingMode.${mode}`))
                    .join(' · ')
                }}
              </span>
            </div>
            <div class="app-action-stack">
              <span
                :class="[
                  'app-badge',
                  catalogState(provider) === 'available'
                    ? 'app-badge--success'
                    : 'app-badge--warning',
                ]"
              >
                {{ t(`social.catalogState.${catalogState(provider)}`) }}
              </span>
              <button
                v-if="catalogState(provider) === 'available'"
                class="pq-button"
                type="button"
                :disabled="!canManageChannels || connecting === provider.provider"
                @click="connect(provider)"
              >
                {{ connecting === provider.provider ? t('social.connecting') : t('social.connect') }}
              </button>
              <fieldset
                v-if="catalogState(provider) === 'available' && provider.capabilities.dynamic_discovery"
                class="composer-provider-fields"
              >
                <legend>{{ t('social.discoveryLegend') }}</legend>
                <label>
                  <span>{{ t('social.discoveryKindLabel') }}</span>
                  <select v-model="discoveryKinds[provider.provider]">
                    <option value="">{{ t('social.chooseDiscoveryKind') }}</option>
                    <option value="instance_origin">{{ t('social.discoveryKind.instance_origin') }}</option>
                    <option value="handle">{{ t('social.discoveryKind.handle') }}</option>
                    <option value="did">{{ t('social.discoveryKind.did') }}</option>
                    <option value="pds_origin">{{ t('social.discoveryKind.pds_origin') }}</option>
                  </select>
                </label>
                <label>
                  <span>{{ t('social.discoveryValueLabel') }}</span>
                  <input
                    v-model="discoveryValues[provider.provider]"
                    type="text"
                    :placeholder="t('social.discoveryValuePlaceholder')"
                  >
                </label>
                <small>{{ t('social.discoveryHelp') }}</small>
              </fieldset>
              <span
                v-if="catalogState(provider) !== 'available'"
                class="app-field__help"
              >
                {{ configurationMessage(provider) }}
              </span>
            </div>
          </li>
        </ul>
      </article>
    </div>
  </section>
</template>
