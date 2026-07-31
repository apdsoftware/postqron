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
  watch,
} from '#imports'
import {
  localeFromAppPath,
} from '../components/core/navigation.ts'
import {
  parseSocialCallbackDocument,
  SOCIAL_OAUTH_CALLBACK_PARAMETERS,
  withoutSocialOAuthCallbackParameters,
} from '../components/core/social-callback.ts'
import {
  useAppSessionState,
  useAppShellApi,
  useAppShellI18n,
  useAppWorkspaceTransitionRevisionState,
  useAppWorkspaceTransitionState,
  useSocialConnectionsApi,
} from '../components/core/use-app-shell.ts'
import { formatDateTime } from '../components/core/preferences.ts'
import {
  normalizeSocialApiError,
  SocialApiError,
} from '../components/core/social-api.ts'
import type {
  SocialBootstrap,
  SocialConnection,
  SocialDiscoveryInput,
  SocialDiscoveryInputKind,
  SocialProviderCatalogEntry,
  SocialProvider,
  SocialSelection,
} from '../components/core/social-connections.ts'
import {
  discoveryKindsForProvider,
  publishingModesForConnection,
} from '../components/core/social-connections.ts'
import type { AppShellMessageKey } from '../components/core/catalogs.ts'

definePageMeta({ layout: 'app-shell' })

const route = useRoute()
const accountApi = useAppShellApi()
const social = useSocialConnectionsApi()
const session = useAppSessionState()
const workspaceTransition = useAppWorkspaceTransitionState()
const workspaceTransitionRevision = useAppWorkspaceTransitionRevisionState()
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
const loadingWorkspace = ref(true)
let loadEpoch = 0
const catalogHeading = ref<{
  focus(): void
  scrollIntoView(options: { behavior: 'smooth', block: 'start' }): void
}>()

const workspaceId = computed(() => session.value?.current_workspace?.id ?? '')
const canManageChannels = computed(() =>
  !workspaceTransition.value
  && workspace.value?.role === 'owner'
  && workspace.value?.status === 'active')

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
  opener: unknown
}

type AsyncWorkspaceContext = Readonly<{
  epoch: number
  permission: 'manage' | 'read-only'
  workspaceId: string
}>

const activePopups = new Set<CallbackPopupHandle>()

function currentPermission(): AsyncWorkspaceContext['permission'] {
  return session.value?.current_workspace?.role === 'owner'
    ? 'manage'
    : 'read-only'
}

function captureAsyncContext(): AsyncWorkspaceContext | undefined {
  const currentWorkspaceId = workspaceId.value
  if (!currentWorkspaceId) {
    return undefined
  }
  return {
    epoch: loadEpoch,
    permission: currentPermission(),
    workspaceId: currentWorkspaceId,
  }
}

function contextIsCurrent(
  context: AsyncWorkspaceContext,
  requireManagePermission = false,
): boolean {
  return context.epoch === loadEpoch
    && context.workspaceId === workspaceId.value
    && context.permission === currentPermission()
    && !workspaceTransition.value
    && (!requireManagePermission
      || (context.permission === 'manage' && canManageChannels.value))
}

function closeActivePopups() {
  for (const popup of activePopups) {
    try {
      popup.close()
    } catch {
      // The context guard still prevents any continuation from changing UI.
    }
  }
  activePopups.clear()
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
  return 'unavailable'
}

function workspaceContextMismatch(): SocialApiError {
  return new SocialApiError({
    code: 'social_workspace_context_mismatch',
    kind: 'unavailable',
    message: 'The social workspace response did not match the active session',
    retryable: true,
  })
}

async function processCallback(context: AsyncWorkspaceContext) {
  const state = queryString(route.query.state)
  const code = queryString(route.query.code)
  const issuer = queryString(route.query.iss)
  const providerError = queryString(route.query.error)
  const hasCallbackParameters = SOCIAL_OAUTH_CALLBACK_PARAMETERS.some(
    parameter => Object.hasOwn(route.query, parameter),
  )
  if (!hasCallbackParameters) {
    return
  }

  // Scrub credentials and provider errors before the first awaited operation.
  // The direct history replacement guarantees removal even if a workspace
  // transition invalidates this callback while router synchronization runs.
  if (import.meta.client) {
    const cleanURL = new globalThis.URL(globalThis.location.href)
    for (const parameter of SOCIAL_OAUTH_CALLBACK_PARAMETERS) {
      cleanURL.searchParams.delete(parameter)
    }
    globalThis.history.replaceState(
      globalThis.history.state,
      '',
      `${cleanURL.pathname}${cleanURL.search}${cleanURL.hash}`,
    )
  }
  await navigateTo({
    path: route.path,
    query: withoutSocialOAuthCallbackParameters(route.query),
    hash: route.hash,
  }, { replace: true })

  if (!state && !code && !issuer && !providerError) {
    return
  }
  if (!contextIsCurrent(context, true)) {
    return
  }
  try {
    const callbackSelection = await social.completeAuthorization({
      state,
      code,
      error: providerError,
      iss: issuer,
    })
    if (!contextIsCurrent(context, true)) {
      return
    }
    selection.value = callbackSelection
    notice.value = undefined
  } catch (error) {
    if (!contextIsCurrent(context, true)) {
      return
    }
    selection.value = undefined
    notice.value = { tone: 'error', key: socialErrorKey(error) }
  }
}

function resetWorkspaceSensitiveState() {
  bootstrap.value = undefined
  connections.value = undefined
  selection.value = undefined
  workspace.value = undefined
  pageState.value = undefined
  notice.value = undefined
  discoveryKinds.value = {}
  discoveryValues.value = {}
  connecting.value = undefined
  reconnecting.value = undefined
  revoking.value = undefined
  selecting.value = undefined
  loadingWorkspace.value = true
}

function invalidateWorkspaceSensitiveState() {
  loadEpoch += 1
  closeActivePopups()
  resetWorkspaceSensitiveState()
}

const { pending, refresh } = useAsyncData('postqron-social-channels', async () => {
  const requestEpoch = ++loadEpoch
  let context: AsyncWorkspaceContext | undefined
  try {
    if (!session.value) {
      const currentSession = await accountApi.session()
      if (requestEpoch !== loadEpoch || workspaceTransition.value) {
        return undefined
      }
      session.value = currentSession
    }
    context = captureAsyncContext()
    if (!context) {
      throw new Error('SOCIAL_WORKSPACE_UNAVAILABLE')
    }
    const [currentWorkspace, currentBootstrap, currentConnections] = await Promise.all([
      accountApi.currentWorkspace(),
      social.bootstrap(context.workspaceId),
      social.list(context.workspaceId),
    ])
    if (!contextIsCurrent(context)) {
      const mismatchStillTargetsCurrentWorkspace = context.epoch === loadEpoch
        && context.workspaceId === workspaceId.value
        && !workspaceTransition.value
      if (mismatchStillTargetsCurrentWorkspace) {
        throw workspaceContextMismatch()
      }
      return undefined
    }
    if (currentWorkspace.id !== context.workspaceId
      || (currentWorkspace.role === 'owner' ? 'manage' : 'read-only') !== context.permission) {
      throw workspaceContextMismatch()
    }
    workspace.value = currentWorkspace
    bootstrap.value = currentBootstrap
    connections.value = currentConnections
    pageState.value = undefined
    loadingWorkspace.value = false
    if (!callbackProcessed.value) {
      callbackProcessed.value = true
      await processCallback(context)
      if (!contextIsCurrent(context)) {
        return undefined
      }
    }
    return { loaded: true }
  } catch (error) {
    const isCurrentRequest = context
      ? contextIsCurrent(context)
      : requestEpoch === loadEpoch && !workspaceTransition.value
    if (isCurrentRequest) {
      resetWorkspaceSensitiveState()
      pageState.value = socialPageState(error)
      loadingWorkspace.value = false
    }
    return undefined
  }
}, { server: false, watch: [workspaceId] })

watch(workspaceId, (current, previous) => {
  if (current !== previous) {
    invalidateWorkspaceSensitiveState()
  }
}, { flush: 'sync' })
watch(workspaceTransition, (targetWorkspaceId) => {
  if (targetWorkspaceId && targetWorkspaceId !== workspaceId.value) {
    invalidateWorkspaceSensitiveState()
  }
}, { flush: 'sync' })
watch(workspaceTransitionRevision, () => {
  invalidateWorkspaceSensitiveState()
  void refresh()
}, { flush: 'sync' })

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

function secureCallbackURL() {
  if (!import.meta.client) {
    return undefined
  }
  const callback = social.callbackURL(globalThis.location.origin)
  if (callback.origin !== globalThis.location.origin) {
    notice.value = { tone: 'error', key: 'social.errorCallbackSameOrigin' }
    return undefined
  }
  return callback
}

function isolatePopup(windowHandle: CallbackPopupHandle): boolean {
  try {
    windowHandle.opener = null
    return windowHandle.opener === null
  } catch {
    return false
  }
}

function discoveryInput(
  provider: SocialProviderCatalogEntry,
): SocialDiscoveryInput | undefined {
  if (!provider.capabilities.dynamic_discovery) {
    return undefined
  }
  const kind = discoveryKinds.value[provider.provider]
  const value = discoveryValues.value[provider.provider]?.trim()
  if (!kind
    || !discoveryKindsForProvider(provider.provider).includes(kind)
    || !value) {
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

async function waitForSelection(
  windowHandle: CallbackPopupHandle,
  context: AsyncWorkspaceContext,
): Promise<SocialSelection> {
  const callbackURL = secureCallbackURL()
  if (!callbackURL) {
    throw normalizeSocialApiError({
      data: {
        code: 'callback_handoff_unavailable',
        message: 'The callback origin is not readable by the application.',
        retryable: false,
      },
    })
  }
  return await new Promise((resolve, reject) => {
    const deadline = Date.now() + 120_000
    const timer = globalThis.setInterval(() => {
      if (!contextIsCurrent(context, true)) {
        globalThis.clearInterval(timer)
        windowHandle.close()
        reject(new Error('SOCIAL_ASYNC_CONTEXT_STALE'))
        return
      }
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
        if (windowHandle.location.origin !== callbackURL.origin
          || windowHandle.location.pathname !== callbackURL.pathname) {
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
  if (!secureCallbackURL()) {
    return
  }
  const context = captureAsyncContext()
  if (!context || !contextIsCurrent(context, true)) {
    return
  }
  const popup = callbackPopup()
  if (!popup) {
    notice.value = { tone: 'error', key: 'social.errorPopupBlocked' }
    return
  }
  if (!isolatePopup(popup)) {
    popup.close()
    notice.value = { tone: 'error', key: 'social.errorPopupIsolation' }
    return
  }
  activePopups.add(popup)
  connecting.value = provider.provider
  notice.value = undefined
  try {
    const discovery = discoveryInput(provider)
    if (provider.capabilities.dynamic_discovery && !discovery) {
      popup.close()
      return
    }
    const authorization = await social.begin(
      context.workspaceId,
      provider.provider,
      discovery,
    )
    if (!contextIsCurrent(context, true)) {
      popup.close()
      return
    }
    popup.location.assign(authorization.authorization_url)
    const popupSelection = await waitForSelection(popup, context)
    if (!contextIsCurrent(context, true)) {
      popup.close()
      return
    }
    selection.value = popupSelection
  } catch (error) {
    popup.close()
    if (contextIsCurrent(context, true)) {
      notice.value = { tone: 'error', key: socialErrorKey(error) }
    }
  } finally {
    activePopups.delete(popup)
    if (contextIsCurrent(context, true)) {
      connecting.value = undefined
    }
  }
}

async function reconnect(connection: SocialConnection) {
  if (!ensureManagePermission()) {
    return
  }
  if (!secureCallbackURL()) {
    return
  }
  const context = captureAsyncContext()
  if (!context || !contextIsCurrent(context, true)) {
    return
  }
  const popup = callbackPopup()
  if (!popup) {
    notice.value = { tone: 'error', key: 'social.errorPopupBlocked' }
    return
  }
  if (!isolatePopup(popup)) {
    popup.close()
    notice.value = { tone: 'error', key: 'social.errorPopupIsolation' }
    return
  }
  activePopups.add(popup)
  reconnecting.value = connection.id
  notice.value = undefined
  try {
    const authorization = await social.reconnect(context.workspaceId, connection.id)
    if (!contextIsCurrent(context, true)) {
      popup.close()
      return
    }
    popup.location.assign(authorization.authorization_url)
    const popupSelection = await waitForSelection(popup, context)
    if (!contextIsCurrent(context, true)) {
      popup.close()
      return
    }
    selection.value = popupSelection
  } catch (error) {
    popup.close()
    if (contextIsCurrent(context, true)) {
      notice.value = { tone: 'error', key: socialErrorKey(error) }
    }
  } finally {
    activePopups.delete(popup)
    if (contextIsCurrent(context, true)) {
      reconnecting.value = undefined
    }
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
  const context = captureAsyncContext()
  if (!context || !contextIsCurrent(context, true)) {
    return
  }
  selecting.value = remoteId
  notice.value = undefined
  try {
    const connection = await social.selectResource(context.workspaceId, {
      selectionId: active.selection_id,
      remoteId,
    })
    if (!contextIsCurrent(context, true)) {
      return
    }
    const currentConnections = await social.list(context.workspaceId)
    if (!contextIsCurrent(context, true)) {
      return
    }
    connections.value = currentConnections
    selection.value = undefined
    notice.value = {
      tone: 'success',
      key: 'social.connectedNotice',
      params: { name: connection.display_name },
    }
  } catch (error) {
    if (contextIsCurrent(context, true)) {
      notice.value = { tone: 'error', key: socialErrorKey(error) }
    }
  } finally {
    if (contextIsCurrent(context, true)) {
      selecting.value = undefined
    }
  }
}

async function disconnect(connection: SocialConnection) {
  if (!ensureManagePermission()) {
    return
  }
  const context = captureAsyncContext()
  if (!context || !contextIsCurrent(context, true)) {
    return
  }
  revoking.value = connection.id
  notice.value = undefined
  try {
    await social.revoke(context.workspaceId, connection.id)
    if (!contextIsCurrent(context, true)) {
      return
    }
    const currentConnections = await social.list(context.workspaceId)
    if (!contextIsCurrent(context, true)) {
      return
    }
    connections.value = currentConnections
    notice.value = { tone: 'success', key: 'social.disconnected' }
  } catch (error) {
    if (contextIsCurrent(context, true)) {
      notice.value = { tone: 'error', key: socialErrorKey(error) }
    }
  } finally {
    if (contextIsCurrent(context, true)) {
      revoking.value = undefined
    }
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
    v-if="(pending || loadingWorkspace) && connections === undefined"
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
                    <option
                      v-for="kind in discoveryKindsForProvider(provider.provider)"
                      :key="kind"
                      :value="kind"
                    >
                      {{ t(`social.discoveryKind.${kind}`) }}
                    </option>
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
