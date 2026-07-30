<script setup lang="ts">
import {
  computed,
  definePageMeta,
  ref,
  useAsyncData,
  useHead,
  useRoute,
} from '#imports'
import { normalizeAppApiError } from '../components/core/api.ts'
import { appRoute, localeFromAppPath } from '../components/core/navigation.ts'
import { formatDateTime } from '../components/core/preferences.ts'
import {
  useAppSessionState,
  useAppShellApi,
  useAppShellI18n,
} from '../components/core/use-app-shell.ts'
import type { AppShellMessageKey } from '../components/core/catalogs.ts'
import type {
  CurrentWorkspace,
  WorkspaceMember,
} from '../components/core/contracts.ts'

definePageMeta({ layout: 'app-shell' })

const api = useAppShellApi()
const route = useRoute()
const session = useAppSessionState()
const { t, locale: uiLocale } = useAppShellI18n()

const workspace = ref<CurrentWorkspace>()
const members = ref<WorkspaceMember[]>([])
const pageState = ref<'access-denied' | 'offline' | 'unavailable'>()
const loadNotice = ref<'not-found' | 'session'>()

const renameName = ref('')
const renaming = ref(false)
const renameFeedback = ref<{ message: string, tone: 'error' | 'success' }>()

const inviteEmail = ref('')
const inviting = ref(false)
const inviteFeedback = ref<{ message: string, tone: 'error' | 'success' }>()

const busyMemberId = ref<string>()
const memberFeedback = ref<{ message: string, tone: 'error' | 'success' }>()

const locale = computed(() => localeFromAppPath(route.fullPath))
const isOwner = computed(() => workspace.value?.role === 'owner')
const ownerCount = computed(() =>
  members.value.filter(member => member.role === 'owner').length)
const currentAccountId = computed(() => session.value?.account.id)

useHead(computed(() => ({
  title: t('documentTitle.workspace'),
})))

const { pending, refresh } = useAsyncData('postqron-workspace', async () => {
  try {
    const [current, memberList] = await Promise.all([
      api.currentWorkspace(),
      api.currentWorkspaceMembers(),
    ])
    workspace.value = current
    members.value = memberList
    renameName.value = current.name
    pageState.value = undefined
    loadNotice.value = undefined
    return current
  } catch (error) {
    workspace.value = undefined
    members.value = []
    classifyLoadError(error)
    return undefined
  }
}, { server: false })

function classifyLoadError(error: unknown): void {
  const normalized = normalizeAppApiError(error)
  if (normalized.kind === 'session') {
    loadNotice.value = 'session'
    pageState.value = undefined
    return
  }
  if (normalized.status === 404) {
    loadNotice.value = 'not-found'
    pageState.value = undefined
    return
  }
  loadNotice.value = undefined
  pageState.value = normalized.kind === 'access-denied'
    ? 'access-denied'
    : normalized.kind === 'offline'
      ? 'offline'
      : 'unavailable'
}

function actionErrorKey(
  error: unknown,
  action: 'invite' | 'remove' | 'rename' | 'role',
): AppShellMessageKey {
  const normalized = normalizeAppApiError(error)
  if (normalized.kind === 'offline') {
    return 'workspace.error.offline'
  }
  switch (normalized.status) {
    case 400:
      return 'workspace.error.invalid'
    case 401:
      return 'workspace.error.session'
    case 403:
      return 'workspace.error.forbidden'
    case 404:
      return 'workspace.error.notFound'
    case 409:
      return action === 'invite'
        ? 'workspace.error.memberLimit'
        : action === 'role' || action === 'remove'
          ? 'workspace.error.lastOwner'
          : 'workspace.error.conflict'
    case 503:
      return 'workspace.error.unavailable'
    default:
      return normalized.status && normalized.status >= 500
        ? 'workspace.error.unavailable'
        : 'workspace.error.unknown'
  }
}

function confirmAction(message: string): boolean {
  return import.meta.client ? globalThis.confirm(message) : true
}

function isSelf(member: WorkspaceMember): boolean {
  return member.account_id === currentAccountId.value
}

function isProtectedOwner(member: WorkspaceMember): boolean {
  return member.role === 'owner' && ownerCount.value <= 1
}

async function submitRename() {
  if (!isOwner.value) {
    return
  }
  renaming.value = true
  renameFeedback.value = undefined
  try {
    const updated = await api.renameCurrentWorkspace(renameName.value)
    workspace.value = updated
    renameName.value = updated.name
    if (session.value?.current_workspace) {
      session.value.current_workspace.name = updated.name
    }
    renameFeedback.value = {
      tone: 'success',
      message: t('workspace.rename.success'),
    }
  } catch (error) {
    renameFeedback.value = {
      tone: 'error',
      message: t(actionErrorKey(error, 'rename')),
    }
  } finally {
    renaming.value = false
  }
}

async function submitInvite() {
  if (!isOwner.value) {
    return
  }
  inviting.value = true
  inviteFeedback.value = undefined
  try {
    const invitation = await api.inviteCurrentWorkspaceMember(inviteEmail.value)
    inviteEmail.value = ''
    inviteFeedback.value = {
      tone: 'success',
      message: t(
        invitation.reissued
          ? 'workspace.invite.reissued'
          : 'workspace.invite.created',
        { date: formatDateTime(invitation.expires_at, uiLocale.value) },
      ),
    }
  } catch (error) {
    inviteFeedback.value = {
      tone: 'error',
      message: t(actionErrorKey(error, 'invite')),
    }
  } finally {
    inviting.value = false
  }
}

async function changeRole(member: WorkspaceMember, role: 'owner' | 'member') {
  if (!isOwner.value || busyMemberId.value) {
    return
  }
  if (role === 'member' && !confirmAction(
    t('workspace.members.confirmDemote', { email: member.email }),
  )) {
    return
  }
  busyMemberId.value = member.id
  memberFeedback.value = undefined
  try {
    await api.changeCurrentWorkspaceMemberRole({ memberId: member.id, role })
    await refresh()
    memberFeedback.value = {
      tone: 'success',
      message: t('workspace.members.roleSuccess'),
    }
  } catch (error) {
    memberFeedback.value = {
      tone: 'error',
      message: t(actionErrorKey(error, 'role')),
    }
  } finally {
    busyMemberId.value = undefined
  }
}

async function removeMember(member: WorkspaceMember) {
  if (!isOwner.value || busyMemberId.value) {
    return
  }
  if (!confirmAction(
    t('workspace.members.confirmRemove', { email: member.email }),
  )) {
    return
  }
  busyMemberId.value = member.id
  memberFeedback.value = undefined
  try {
    await api.removeCurrentWorkspaceMember(member.id)
    await refresh()
    memberFeedback.value = {
      tone: 'success',
      message: t('workspace.members.removeSuccess'),
    }
  } catch (error) {
    memberFeedback.value = {
      tone: 'error',
      message: t(actionErrorKey(error, 'remove')),
    }
  } finally {
    busyMemberId.value = undefined
  }
}

async function retry() {
  await refresh()
}
</script>

<template>
  <AppState
    v-if="pending && !workspace"
    kind="loading"
  />
  <section
    v-else-if="loadNotice === 'session'"
    class="app-state app-state--access-denied"
    role="alert"
  >
    <span
      class="app-state__icon"
      aria-hidden="true"
    >!</span>
    <div>
      <h1>{{ t('workspace.load.sessionTitle') }}</h1>
      <p>{{ t('workspace.load.sessionDescription') }}</p>
      <NuxtLink
        class="pq-button pq-button--secondary"
        :to="appRoute(locale, 'entry')"
      >
        {{ t('workspace.load.signIn') }}
      </NuxtLink>
    </div>
  </section>
  <section
    v-else-if="loadNotice === 'not-found'"
    class="app-state app-state--empty"
    role="status"
    aria-live="polite"
  >
    <span
      class="app-state__icon"
      aria-hidden="true"
    >○</span>
    <div>
      <h1>{{ t('workspace.load.notFoundTitle') }}</h1>
      <p>{{ t('workspace.load.notFoundDescription') }}</p>
      <button
        class="pq-button pq-button--secondary"
        type="button"
        @click="retry"
      >
        {{ t('state.retry') }}
      </button>
    </div>
  </section>
  <AppState
    v-else-if="pageState"
    :kind="pageState"
    action
    @retry="retry"
  />
  <section
    v-else-if="workspace"
    class="app-page"
  >
    <p class="app-eyebrow">
      {{ t('workspace.eyebrow') }}
    </p>
    <h1>{{ t('workspace.title') }}</h1>
    <p class="app-page__lead">
      {{ t('workspace.description') }}
    </p>

    <div class="app-page__stack">
      <article class="app-card">
        <div class="app-card__header">
          <span class="app-card__eyebrow">{{ t('workspace.overview.section') }}</span>
          <h2>{{ t('workspace.overview.title') }}</h2>
        </div>
        <dl class="app-detail-list">
          <div class="app-inline-meta">
            <dt>{{ t('workspace.overview.name') }}</dt>
            <dd>{{ workspace.name }}</dd>
          </div>
          <div class="app-inline-meta">
            <dt>{{ t('workspace.overview.status') }}</dt>
            <dd>
              <span
                class="app-badge"
                :class="workspace.status === 'active' ? 'app-badge--success' : 'app-badge--warning'"
              >
                {{ t(`workspace.status.${workspace.status}`) }}
              </span>
            </dd>
          </div>
          <div class="app-inline-meta">
            <dt>{{ t('workspace.overview.role') }}</dt>
            <dd>
              <span class="app-badge app-badge--info">
                {{ t(`workspace.role.${workspace.role}`) }}
              </span>
            </dd>
          </div>
          <div class="app-inline-meta">
            <dt>{{ t('workspace.overview.created') }}</dt>
            <dd>{{ formatDateTime(workspace.created_at, uiLocale.value) }}</dd>
          </div>
          <div class="app-inline-meta">
            <dt>{{ t('workspace.overview.updated') }}</dt>
            <dd>{{ formatDateTime(workspace.updated_at, uiLocale.value) }}</dd>
          </div>
          <div class="app-inline-meta">
            <dt>{{ t('workspace.overview.plan') }}</dt>
            <dd>
              <NuxtLink
                class="app-inline-link"
                :to="appRoute(locale, 'plan')"
              >
                {{ t('workspace.overview.planLink') }}
              </NuxtLink>
            </dd>
          </div>
        </dl>
      </article>

      <article
        v-if="isOwner"
        class="app-card"
      >
        <div class="app-card__header">
          <span class="app-card__eyebrow">{{ t('workspace.rename.section') }}</span>
          <h2>{{ t('workspace.rename.title') }}</h2>
        </div>
        <form
          class="app-form-grid"
          @submit.prevent="submitRename"
        >
          <label class="app-field">
            <span>{{ t('workspace.rename.label') }}</span>
            <input
              v-model="renameName"
              type="text"
              required
              minlength="1"
              maxlength="80"
              :placeholder="t('workspace.rename.placeholder')"
              autocomplete="off"
            >
          </label>
          <button
            class="pq-button"
            type="submit"
            :disabled="renaming"
          >
            {{ renaming ? t('workspace.rename.saving') : t('workspace.rename.submit') }}
          </button>
        </form>
        <p
          v-if="renameFeedback"
          class="app-inline-alert"
          :data-success="renameFeedback.tone === 'success'"
          :role="renameFeedback.tone === 'success' ? 'status' : 'alert'"
        >
          {{ renameFeedback.message }}
        </p>
      </article>

      <article class="app-card">
        <div class="app-card__header">
          <span class="app-card__eyebrow">{{ t('workspace.members.section') }}</span>
          <h2>{{ t('workspace.members.title') }}</h2>
        </div>
        <p class="app-inline-note">
          {{ isOwner ? t('workspace.members.ownerHint') : t('workspace.members.memberHint') }}
        </p>
        <p
          v-if="isOwner"
          class="app-inline-note"
        >
          {{ t('workspace.members.lastOwnerNote') }}
        </p>
        <AppState
          v-if="members.length === 0"
          kind="empty"
        />
        <ul
          v-else
          class="app-member-list"
        >
          <li
            v-for="member in members"
            :key="member.id"
          >
            <div class="app-member-list__meta">
              <strong>{{ member.email }}</strong>
              <span>
                {{ t(`workspace.role.${member.role}`) }}
                · {{ t('workspace.members.joined') }}
                {{ formatDateTime(member.created_at, uiLocale.value) }}
              </span>
            </div>
            <div class="app-member-list__badges">
              <span
                v-if="isSelf(member)"
                class="app-badge app-badge--info"
              >{{ t('workspace.you') }}</span>
              <span
                class="app-badge"
                :class="member.role === 'owner' ? 'app-badge--success' : 'app-badge--info'"
              >{{ t(`workspace.role.${member.role}`) }}</span>
            </div>
            <div
              v-if="isOwner"
              class="app-member-list__actions"
            >
              <button
                v-if="member.role === 'member'"
                class="pq-button pq-button--secondary"
                type="button"
                :disabled="Boolean(busyMemberId)"
                @click="changeRole(member, 'owner')"
              >
                {{ busyMemberId === member.id ? t('workspace.members.promoting') : t('workspace.members.promote') }}
              </button>
              <button
                v-else
                class="pq-button pq-button--secondary"
                type="button"
                :disabled="Boolean(busyMemberId) || isProtectedOwner(member)"
                @click="changeRole(member, 'member')"
              >
                {{ busyMemberId === member.id ? t('workspace.members.demoting') : t('workspace.members.demote') }}
              </button>
              <button
                class="pq-button pq-button--secondary"
                type="button"
                :disabled="Boolean(busyMemberId) || isProtectedOwner(member)"
                @click="removeMember(member)"
              >
                {{ busyMemberId === member.id ? t('workspace.members.removing') : t('workspace.members.remove') }}
              </button>
            </div>
          </li>
        </ul>
        <p
          v-if="memberFeedback"
          class="app-inline-alert"
          :data-success="memberFeedback.tone === 'success'"
          :role="memberFeedback.tone === 'success' ? 'status' : 'alert'"
        >
          {{ memberFeedback.message }}
        </p>
      </article>

      <article
        v-if="isOwner"
        class="app-card"
      >
        <div class="app-card__header">
          <span class="app-card__eyebrow">{{ t('workspace.invite.section') }}</span>
          <h2>{{ t('workspace.invite.title') }}</h2>
        </div>
        <p>{{ t('workspace.invite.description') }}</p>
        <form
          class="app-form-grid"
          @submit.prevent="submitInvite"
        >
          <label class="app-field">
            <span>{{ t('workspace.invite.label') }}</span>
            <input
              v-model="inviteEmail"
              type="email"
              required
              :placeholder="t('workspace.invite.placeholder')"
              autocomplete="off"
            >
          </label>
          <button
            class="pq-button"
            type="submit"
            :disabled="inviting"
          >
            {{ inviting ? t('workspace.invite.sending') : t('workspace.invite.submit') }}
          </button>
        </form>
        <p
          v-if="inviteFeedback"
          class="app-inline-alert"
          :data-success="inviteFeedback.tone === 'success'"
          :role="inviteFeedback.tone === 'success' ? 'status' : 'alert'"
        >
          {{ inviteFeedback.message }}
        </p>
      </article>
    </div>
  </section>
</template>
