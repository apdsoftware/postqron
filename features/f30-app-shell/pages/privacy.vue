<script setup lang="ts">
import {
  computed,
  definePageMeta,
  navigateTo,
  ref,
  useAsyncData,
  useHead,
  useRoute,
} from '#imports'
import {
  useAccountDeletionCancellationState,
  appStateKindFromError,
  useAppAccountAreaState,
  useAppShellApi,
  useAppShellI18n,
} from '../components/core/use-app-shell.ts'
import {
  accountDeletionCancellationRoute,
  localeFromAppPath,
} from '../components/core/navigation.ts'
import { formatDateTime } from '../components/core/preferences.ts'
import {
  buildAccountDeletionOwnershipActions,
  type DeletionRequest,
  type ExportDownload,
  type ExportRequest,
} from '../components/core/contracts.ts'

definePageMeta({ layout: 'app-shell' })

const api = useAppShellApi()
const route = useRoute()
const accountArea = useAppAccountAreaState()
const accountDeletion = useAccountDeletionCancellationState()
const { t, locale: uiLocale } = useAppShellI18n()
const exportRequest = ref<ExportRequest>()
const exportDownload = ref<ExportDownload>()
const deletionRequest = ref<DeletionRequest>()
const working = ref<'account-delete' | 'account-export' | 'cancel-delete' | 'workspace-delete' | 'workspace-export' | 'download'>()
const feedback = ref<'error' | 'ownership-unavailable' | 'saved'>()
const pageState = ref<'access-denied' | 'offline' | 'unavailable'>()

useHead(computed(() => ({
  title: t('documentTitle.privacy'),
})))

const { pending, refresh } = useAsyncData('postqron-account-privacy', async () => {
  try {
    accountArea.value = await api.accountArea()
    pageState.value = undefined
    return accountArea.value
  } catch (error) {
    accountArea.value = undefined
    pageState.value = appStateKindFromError(error)
    return undefined
  }
}, { server: false })

const ownerWorkspace = computed(() =>
  accountArea.value?.workspaces.find(item => item.workspace.role === 'owner'))
const ownerWorkspaces = computed(() =>
  accountArea.value?.workspaces.filter(item =>
    item.workspace.role === 'owner') ?? [])

function confirmAction(message: string): boolean {
  return import.meta.client ? globalThis.confirm(message) : true
}

async function requestExport(scope: 'account' | 'workspace') {
  working.value = scope === 'account' ? 'account-export' : 'workspace-export'
  feedback.value = undefined
  try {
    exportRequest.value = await api.requestExport({
      scope,
      workspaceId: scope === 'workspace' ? ownerWorkspace.value?.workspace.id : undefined,
    })
    exportDownload.value = undefined
    feedback.value = 'saved'
  } catch {
    feedback.value = 'error'
  } finally {
    working.value = undefined
  }
}

async function fetchDownload() {
  if (!exportRequest.value) {
    return
  }
  working.value = 'download'
  feedback.value = undefined
  try {
    exportDownload.value = await api.downloadExport(exportRequest.value.id)
    feedback.value = 'saved'
  } catch {
    feedback.value = 'error'
  } finally {
    working.value = undefined
  }
}

async function requestDeletion(scope: 'account' | 'workspace') {
  let ownershipActions: DeletionRequest['ownership']['actions'] | undefined
  if (scope === 'account') {
    try {
      ownershipActions = buildAccountDeletionOwnershipActions(accountArea.value)
    } catch {
      feedback.value = 'ownership-unavailable'
      return
    }
  }
  const message = scope === 'account'
    ? ownerWorkspaces.value.length > 0
      ? t('privacy.confirmAccountDeletion', {
          workspaces: ownerWorkspaces.value
            .map(item => `• ${item.workspace.name}`)
            .join('\n'),
        })
      : t('privacy.confirmAccountDeletionNoOwnedWorkspaces')
    : t('privacy.confirmWorkspaceDeletion', {
        workspace: ownerWorkspace.value?.workspace.name ?? '',
      })
  if (!confirmAction(message)) {
    return
  }
  working.value = scope === 'account' ? 'account-delete' : 'workspace-delete'
  feedback.value = undefined
  try {
    if (scope === 'account') {
      await api.issueAccountDeletionCancelCapability()
    }
    const deletion = await api.requestDeletion({
      scope,
      workspaceId: scope === 'workspace' ? ownerWorkspace.value?.workspace.id : undefined,
      ownershipActions,
    })
    if (scope === 'account') {
      accountDeletion.value = {
        requestId: deletion.id,
        status: deletion.status,
        graceEndsAt: deletion.grace_ends_at,
      }
      await navigateTo(accountDeletionCancellationRoute(
        localeFromAppPath(route.fullPath),
        deletion.id,
      ))
      return
    }
    deletionRequest.value = deletion
    feedback.value = 'saved'
  } catch {
    feedback.value = 'error'
  } finally {
    working.value = undefined
  }
}

async function cancelDeletion() {
  if (!deletionRequest.value) {
    return
  }
  working.value = 'cancel-delete'
  feedback.value = undefined
  try {
    await api.cancelWorkspaceDeletion(deletionRequest.value.id)
    deletionRequest.value = undefined
    feedback.value = 'saved'
  } catch {
    feedback.value = 'error'
  } finally {
    working.value = undefined
  }
}

async function retry() {
  await refresh()
}
</script>

<template>
  <AppState
    v-if="pending && !accountArea"
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
  >
    <p class="app-eyebrow">
      {{ t('privacy.eyebrow') }}
    </p>
    <h1>{{ t('privacy.title') }}</h1>
    <p class="app-page__lead">
      {{ t('privacy.description') }}
    </p>

    <div class="app-page__grid">
      <article class="app-card">
        <div class="app-card__header">
          <span class="app-card__eyebrow">{{ t('privacy.exportAccount') }}</span>
          <h2>{{ t('privacy.exportAccountTitle') }}</h2>
        </div>
        <p>{{ t('privacy.exportDescription') }}</p>
        <button
          class="pq-button"
          type="button"
          :disabled="working === 'account-export'"
          @click="requestExport('account')"
        >
          {{ working === 'account-export' ? t('privacy.requesting') : t('privacy.requestExport') }}
        </button>
      </article>
      <article class="app-card">
        <div class="app-card__header">
          <span class="app-card__eyebrow">{{ t('privacy.exportWorkspace') }}</span>
          <h2>{{ ownerWorkspace?.workspace.name || t('privacy.workspaceUnavailable') }}</h2>
        </div>
        <p>{{ t('privacy.exportWorkspaceDescription') }}</p>
        <button
          class="pq-button"
          type="button"
          :disabled="!ownerWorkspace || working === 'workspace-export'"
          @click="requestExport('workspace')"
        >
          {{ working === 'workspace-export' ? t('privacy.requesting') : t('privacy.requestExport') }}
        </button>
      </article>
    </div>

    <article
      v-if="exportRequest"
      class="app-card"
    >
      <div class="app-card__header">
        <span class="app-card__eyebrow">{{ t('privacy.exportStatus') }}</span>
      </div>
      <dl class="app-detail-list">
        <div class="app-inline-meta">
          <dt>{{ t('privacy.requestStatusLabel') }}</dt>
          <dd>
            <span class="app-badge app-badge--info">{{ exportRequest.status }}</span>
          </dd>
        </div>
        <div class="app-inline-meta">
          <dt>{{ t('privacy.requestedAtLabel') }}</dt>
          <dd>{{ formatDateTime(exportRequest.requested_at, uiLocale.value) }}</dd>
        </div>
      </dl>
      <button
        class="pq-button"
        type="button"
        :disabled="working === 'download'"
        @click="fetchDownload"
      >
        {{ working === 'download' ? t('privacy.downloading') : t('privacy.download') }}
      </button>
      <p
        v-if="exportDownload"
        class="app-inline-note"
      >
        {{ exportDownload.url }}
      </p>
    </article>

    <article class="app-card app-card--danger">
      <div class="app-card__header">
        <span class="app-card__eyebrow">{{ t('privacy.dangerZone') }}</span>
        <h2>{{ t('privacy.dangerZoneTitle') }}</h2>
      </div>
      <p>{{ t('privacy.dangerZoneDescription') }}</p>

      <section class="app-action-stack">
        <div class="app-card__header">
          <strong>{{ t('privacy.deleteAccount') }}</strong>
        </div>
        <p>{{ t('privacy.deleteDescription') }}</p>
        <p v-if="ownerWorkspaces.length">
          {{ t('privacy.accountDeletionOwnedWorkspaces') }}
        </p>
        <ul
          v-if="ownerWorkspaces.length"
          class="app-list"
        >
          <li
            v-for="item in ownerWorkspaces"
            :key="item.workspace.id"
          >
            {{ item.workspace.name }}
          </li>
        </ul>
        <p
          v-else
          class="app-inline-note"
        >
          {{ t('privacy.accountDeletionNoOwnedWorkspaces') }}
        </p>
        <button
          class="pq-button pq-button--secondary"
          type="button"
          :disabled="!accountArea || working === 'account-delete'"
          @click="requestDeletion('account')"
        >
          {{ working === 'account-delete' ? t('privacy.requesting') : t('privacy.requestDelete') }}
        </button>
      </section>

      <section class="app-action-stack">
        <div class="app-card__header">
          <strong>{{ t('privacy.deleteWorkspace') }}</strong>
        </div>
        <p>{{ ownerWorkspace?.workspace.name || t('privacy.workspaceUnavailable') }}</p>
        <button
          class="pq-button pq-button--secondary"
          type="button"
          :disabled="!ownerWorkspace || working === 'workspace-delete'"
          @click="requestDeletion('workspace')"
        >
          {{ working === 'workspace-delete' ? t('privacy.requesting') : t('privacy.requestDelete') }}
        </button>
      </section>
    </article>

    <article
      v-if="deletionRequest"
      class="app-card"
    >
      <div class="app-card__header">
        <span class="app-card__eyebrow">{{ t('privacy.deletionStatus') }}</span>
      </div>
      <dl class="app-detail-list">
        <div class="app-inline-meta">
          <dt>{{ t('privacy.requestStatusLabel') }}</dt>
          <dd>
            <span class="app-badge app-badge--warning">{{ deletionRequest.status }}</span>
          </dd>
        </div>
        <div class="app-inline-meta">
          <dt>{{ t('privacy.graceEndsLabel') }}</dt>
          <dd>{{ formatDateTime(deletionRequest.grace_ends_at, uiLocale.value) }}</dd>
        </div>
      </dl>
      <button
        class="pq-button pq-button--secondary"
        type="button"
        :disabled="working === 'cancel-delete'"
        @click="cancelDeletion"
      >
        {{ working === 'cancel-delete' ? t('privacy.cancelling') : t('privacy.cancelDeletion') }}
      </button>
    </article>

    <p class="app-inline-note">
      {{ t('privacy.statusNote') }}
    </p>

    <p
      v-if="feedback"
      class="app-inline-alert"
      :data-success="feedback === 'saved'"
      role="status"
    >
      {{
        feedback === 'saved'
          ? t('privacy.saved')
          : feedback === 'ownership-unavailable'
            ? t('privacy.accountDeletionOwnershipUnavailable')
            : t('privacy.error')
      }}
    </p>
  </section>
</template>
