<script setup lang="ts">
import {
  computed,
  definePageMeta,
  navigateTo,
  onBeforeUnmount,
  ref,
  useAsyncData,
  useHead,
  useRoute,
  watch,
} from '#imports'
import type { AppShellMessageKey } from '../components/core/catalogs.ts'
import {
  emptyDraftContent,
  type ComposerDestination,
  type ComposerMedia,
  type ContentCapability,
  type DraftContent,
  type DraftView,
  type ScheduleInput,
  type ScheduledPost,
} from '../components/core/editorial-contracts.ts'
import {
  EditorialApiError,
  normalizeEditorialApiError,
} from '../components/core/editorial-api.ts'
import {
  applyDestinationCapability,
  setDestinationField,
} from '../components/core/editorial-form.ts'
import {
  immediateScheduleInput,
  submitScheduledDraft,
} from '../components/core/editorial-submit.ts'
import {
  appRoute,
  localeFromAppPath,
} from '../components/core/navigation.ts'
import type { SocialConnection } from '../components/core/social-connections.ts'
import {
  detectedTimeZone,
  supportedTimeZones,
} from '../components/core/timezones.ts'
import {
  useAppSessionState,
  useAppShellApi,
  useAppShellI18n,
  useComposerApi,
  useSchedulingApi,
  useSocialConnectionsApi,
} from '../components/core/use-app-shell.ts'

definePageMeta({ layout: 'app-shell' })

const route = useRoute()
const session = useAppSessionState()
const accountApi = useAppShellApi()
const socialApi = useSocialConnectionsApi()
const composerApi = useComposerApi()
const schedulingApi = useSchedulingApi()
const { t } = useAppShellI18n()
const locale = computed(() => localeFromAppPath(route.fullPath))
const workspaceId = computed(() => session.value?.current_workspace?.id ?? '')

const connections = ref<SocialConnection[]>()
const capabilityCatalog = ref<Awaited<ReturnType<typeof composerApi.capabilities>>>()
const draftView = ref<DraftView>()
const editingPost = ref<ScheduledPost>()
const content = ref<DraftContent>(emptyDraftContent())
const pageState = ref<'access-denied' | 'offline' | 'unavailable'>()
const notice = ref<{
  key: AppShellMessageKey
  params?: Readonly<Record<string, string | number>>
  tone: 'error' | 'success'
}>()
const saveState = ref<'idle' | 'saving' | 'saved' | 'error'>('idle')
const uploading = ref(false)
const action = ref<'publish' | 'schedule'>()
const scheduleOpen = ref(false)
const hydrated = ref(false)
let autosaveTimer: ReturnType<typeof globalThis.setTimeout> | undefined
let saveChain: Promise<unknown> = Promise.resolve()

const detectedTimezone = detectedTimeZone()
const timezoneOptions = supportedTimeZones(detectedTimezone)
const scheduleDateTime = ref(wallClock(new Date(Date.now() + 60 * 60 * 1000), detectedTimezone))
const scheduleTimezone = ref(detectedTimezone)
const utcOffsetMinutes = ref<string>('')

useHead(computed(() => ({
  title: t('documentTitle.publish'),
})))

function queryValue(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function wallClock(date: Date, timeZone: string): string {
  const parts = new Intl.DateTimeFormat('en-CA', {
    day: '2-digit',
    hour: '2-digit',
    hour12: false,
    minute: '2-digit',
    month: '2-digit',
    timeZone,
    year: 'numeric',
  }).formatToParts(date)
  const value = Object.fromEntries(parts.map(part => [part.type, part.value]))
  return `${value.year}-${value.month}-${value.day}T${value.hour}:${value.minute}`
}

function contentSnapshot(): DraftContent {
  return {
    text: content.value.text,
    link: content.value.link,
    media: content.value.media.map(media => ({ ...media })),
    thread: content.value.thread.map(item => ({
      text: item.text,
      media_ids: [...item.media_ids],
    })),
    destinations: content.value.destinations.map(destination => ({
      ...destination,
      fields: destination.fields ? { ...destination.fields } : undefined,
      media_ids: destination.media_ids ? [...destination.media_ids] : destination.media_ids,
      thread_override: destination.thread_override?.map(item => ({
        text: item.text,
        media_ids: [...item.media_ids],
      })),
    })),
  }
}

function editorialErrorKey(error: unknown): AppShellMessageKey {
  switch (normalizeEditorialApiError(error).kind) {
    case 'access-denied':
    case 'session':
      return 'editorial.error.accessDenied'
    case 'conflict':
      return 'editorial.error.conflict'
    case 'dependency':
      return 'editorial.error.dependency'
    case 'invalid':
      return 'editorial.error.invalid'
    case 'offline':
      return 'editorial.error.offline'
    case 'not-found':
      return 'editorial.error.notFound'
    default:
      return 'editorial.error.unavailable'
  }
}

function stateFromError(
  error: unknown,
): 'access-denied' | 'offline' | 'unavailable' {
  const kind = normalizeEditorialApiError(error).kind
  if (kind === 'access-denied' || kind === 'session') {
    return 'access-denied'
  }
  return kind === 'offline' ? 'offline' : 'unavailable'
}

const { pending, refresh } = useAsyncData('postqron-publish', async () => {
  hydrated.value = false
  try {
    if (!session.value) {
      session.value = await accountApi.session()
    }
    const [loadedConnections, loadedCapabilities] = await Promise.all([
      socialApi.list(workspaceId.value),
      composerApi.capabilities(workspaceId.value),
    ])
    connections.value = loadedConnections.filter(connection => connection.status !== 'revoked')
    capabilityCatalog.value = loadedCapabilities

    const postId = queryValue(route.query.post)
    const draftId = queryValue(route.query.draft)
    if (postId) {
      editingPost.value = await schedulingApi.get(workspaceId.value, postId)
      draftView.value = await composerApi.getDraft(
        workspaceId.value,
        editingPost.value.draft_id,
      )
      content.value = contentSnapshotFrom(draftView.value.draft.content)
    } else if (draftId) {
      draftView.value = await composerApi.getDraft(workspaceId.value, draftId)
      content.value = contentSnapshotFrom(draftView.value.draft.content)
    } else {
      draftView.value = undefined
      editingPost.value = undefined
      content.value = emptyDraftContent()
    }
    pageState.value = undefined
    hydrated.value = true
    return true
  } catch (error) {
    pageState.value = stateFromError(error)
    return false
  }
}, { server: false, watch: [workspaceId] })

function contentSnapshotFrom(source: DraftContent): DraftContent {
  content.value = source
  return contentSnapshot()
}

const activeConnections = computed(() =>
  (connections.value ?? []).filter(connection => connection.status === 'connected'),
)

function capabilitiesFor(connection: SocialConnection): ContentCapability[] {
  return (capabilityCatalog.value?.capabilities ?? [])
    .filter(capability =>
      capability.available && capability.channel_type === connection.resource_type,
    )
}

function selectedDestination(connectionId: string): ComposerDestination | undefined {
  return content.value.destinations.find(
    destination => destination.channel_id === connectionId,
  )
}

function toggleDestination(connection: SocialConnection, selected: boolean) {
  const existing = selectedDestination(connection.id)
  if (!selected) {
    content.value.destinations = content.value.destinations.filter(
      destination => destination.channel_id !== connection.id,
    )
    return
  }
  if (existing) {
    return
  }
  const capability = capabilitiesFor(connection)[0]
  if (!capability) {
    notice.value = { tone: 'error', key: 'composer.destinationUnavailable' }
    return
  }
  content.value.destinations.push({
    id: `destination_${connection.id}`,
    channel_id: connection.id,
    channel_type: connection.resource_type,
    capability_id: capability.id,
    format: capability.format,
    fields: Object.fromEntries(
      (capability.fields ?? []).map(field => [field.name, '']),
    ),
  })
}

function changeCapability(connection: SocialConnection, capabilityId: string) {
  const destination = selectedDestination(connection.id)
  const capability = capabilitiesFor(connection)
    .find(candidate => candidate.id === capabilityId)
  if (!destination || !capability) {
    return
  }
  applyDestinationCapability(destination, capability)
}

function selectedCapability(connection: SocialConnection): ContentCapability | undefined {
  const destination = selectedDestination(connection.id)
  return destination
    ? capabilitiesFor(connection).find(
        capability => capability.id === destination.capability_id,
      )
    : undefined
}

function personalize(connectionId: string, text: string) {
  const destination = selectedDestination(connectionId)
  if (destination) {
    destination.text_override = text
  }
}

function updateProviderField(
  connectionId: string,
  name: string,
  value: string,
) {
  const destination = selectedDestination(connectionId)
  if (destination) {
    setDestinationField(destination, name, value)
  }
}

async function performPersist(autosave: boolean): Promise<DraftView | undefined> {
  saveState.value = 'saving'
  const snapshot = contentSnapshot()
  try {
    const current = draftView.value
    draftView.value = current
      ? await composerApi.saveDraft(workspaceId.value, current.draft.id, {
          autosaveKey: autosave
            ? `autosave-${current.draft.id}-${Date.now()}`
            : undefined,
          content: snapshot,
          expectedRevision: current.draft.revision,
        })
      : await composerApi.createDraft(workspaceId.value, snapshot)
    saveState.value = 'saved'
    if (!autosave) {
      notice.value = { tone: 'success', key: 'composer.saved' }
    }
    return draftView.value
  } catch (error) {
    saveState.value = 'error'
    notice.value = { tone: 'error', key: editorialErrorKey(error) }
    return undefined
  }
}

function persist(autosave: boolean): Promise<DraftView | undefined> {
  const save = saveChain.then(() => performPersist(autosave))
  saveChain = save.then(() => undefined, () => undefined)
  return save
}

watch(content, () => {
  if (!hydrated.value) {
    return
  }
  saveState.value = 'idle'
  if (autosaveTimer) {
    globalThis.clearTimeout(autosaveTimer)
  }
  autosaveTimer = globalThis.setTimeout(() => void persist(true), 900)
}, { deep: true })

onBeforeUnmount(() => {
  if (autosaveTimer) {
    globalThis.clearTimeout(autosaveTimer)
  }
})

async function uploadMedia(event: unknown) {
  const input = (event as {
    target?: {
      files?: { readonly [index: number]: unknown, readonly length: number }
      value: string
    }
  }).target
  if (!input) {
    return
  }
  const file = input.files?.[0]
  input.value = ''
  if (!file || !(file instanceof globalThis.File)) {
    return
  }
  uploading.value = true
  notice.value = undefined
  try {
    const authorization = await composerApi.authorizeMedia(workspaceId.value, file)
    const upload = await globalThis.fetch(authorization.upload_url, {
      body: file,
      credentials: 'omit',
      headers: authorization.upload_headers,
      method: 'PUT',
    })
    if (!upload.ok) {
      throw new EditorialApiError({
        code: 'media_upload_failed',
        kind: 'unavailable',
        message: 'The object-store upload failed',
        retryable: true,
        status: upload.status,
      })
    }
    const media = await composerApi.completeMedia(workspaceId.value, authorization.id)
    content.value.media.push(media)
    notice.value = { tone: 'success', key: 'composer.mediaReady' }
  } catch (error) {
    notice.value = { tone: 'error', key: editorialErrorKey(error) }
  } finally {
    uploading.value = false
  }
}

async function removeMedia(media: ComposerMedia) {
  content.value.media = content.value.media.filter(item => item.id !== media.id)
  for (const destination of content.value.destinations) {
    if (destination.media_ids) {
      destination.media_ids = destination.media_ids.filter(id => id !== media.id)
    }
  }
  const saved = await persist(false)
  if (saved) {
    try {
      await composerApi.deleteMedia(workspaceId.value, media.id)
    } catch {
      // F6 owns lifecycle cleanup. The saved draft already detached the media.
    }
  }
}

async function validateAndSave(): Promise<DraftView | undefined> {
  notice.value = undefined
  if (content.value.destinations.length === 0) {
    notice.value = { tone: 'error', key: 'composer.destinationRequired' }
    return undefined
  }
  const saved = await persist(false)
  if (!saved) {
    return undefined
  }
  try {
    const validation = await composerApi.validateDraft(
      workspaceId.value,
      saved.draft.id,
    )
    draftView.value = { draft: saved.draft, validation }
    if (!validation.valid) {
      notice.value = { tone: 'error', key: 'composer.validationFailed' }
      return undefined
    }
    if (editingPost.value) {
      editingPost.value = await schedulingApi.edit(
        workspaceId.value,
        editingPost.value.id,
        {
          channelIds: saved.draft.content.destinations.map(
            destination => destination.channel_id,
          ),
          draftId: saved.draft.id,
          expectedRevision: editingPost.value.revision,
        },
      )
    }
    return saved
  } catch (error) {
    notice.value = { tone: 'error', key: editorialErrorKey(error) }
    return undefined
  }
}

function scheduleInput(): ScheduleInput | undefined {
  const timezone = scheduleTimezone.value.trim()
  if (!scheduleDateTime.value || !timezone) {
    notice.value = { tone: 'error', key: 'composer.scheduleRequired' }
    return undefined
  }
  const offset = utcOffsetMinutes.value.trim()
  if (offset && (!/^-?[0-9]+$/u.test(offset) || Math.abs(Number(offset)) > 1080)) {
    notice.value = { tone: 'error', key: 'composer.offsetInvalid' }
    return undefined
  }
  return {
    local_date_time: scheduleDateTime.value,
    time_zone: timezone,
    ...(offset ? { utc_offset_minutes: Number(offset) } : {}),
  }
}

async function submitSchedule(immediate: boolean) {
  action.value = immediate ? 'publish' : 'schedule'
  try {
    const saved = await validateAndSave()
    if (!saved) {
      return
    }
    const input = immediate
      ? immediateScheduleInput(new Date(), scheduleTimezone.value)
      : scheduleInput()
    if (!input) {
      return
    }
    editingPost.value = await submitScheduledDraft(schedulingApi, {
      channelIds: saved.draft.content.destinations.map(
        destination => destination.channel_id,
      ),
      draftId: saved.draft.id,
      existingPost: editingPost.value,
      scheduledAt: input,
      workspaceId: workspaceId.value,
    })
    await navigateTo(appRoute(locale.value, 'calendar'))
  } catch (error) {
    notice.value = { tone: 'error', key: editorialErrorKey(error) }
  } finally {
    action.value = undefined
  }
}

function validationErrors(destinationId?: string) {
  const validation = draftView.value?.validation
  if (!validation) {
    return []
  }
  return [
    ...validation.errors,
    ...validation.destinations
      .filter(destination => !destinationId || destination.destination_id === destinationId)
      .flatMap(destination => destination.errors),
  ].filter(error => !destinationId || !error.destination_id || error.destination_id === destinationId)
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
    @retry="refresh"
  />
  <section
    v-else
    class="app-page composer-page"
  >
    <p class="app-eyebrow">
      {{ t('composer.eyebrow') }}
    </p>
    <div class="app-page__title-row">
      <div>
        <h1>{{ editingPost ? t('composer.editTitle') : t('composer.title') }}</h1>
        <p class="app-page__lead">
          {{ t('composer.description') }}
        </p>
      </div>
      <span
        class="composer-save-state"
        role="status"
        aria-live="polite"
      >
        {{ t(`composer.saveState.${saveState}`) }}
      </span>
    </div>

    <p
      v-if="notice"
      class="app-inline-alert"
      :data-success="notice.tone === 'success'"
      :role="notice.tone === 'success' ? 'status' : 'alert'"
    >
      {{ t(notice.key, notice.params ?? {}) }}
    </p>

    <article
      v-if="activeConnections.length === 0"
      class="app-card app-card--accent composer-empty"
    >
      <span class="app-card__eyebrow">{{ t('composer.emptyEyebrow') }}</span>
      <h2>{{ t('composer.emptyTitle') }}</h2>
      <p>{{ t('composer.emptyDescription') }}</p>
      <NuxtLink
        class="pq-button"
        :to="appRoute(locale, 'social-channels')"
      >
        {{ t('composer.addChannel') }}
      </NuxtLink>
    </article>

    <form
      v-else
      class="composer-layout"
      @submit.prevent
    >
      <div class="composer-main">
        <article class="app-card">
          <div class="app-card__header">
            <span class="app-card__eyebrow">{{ t('composer.contentEyebrow') }}</span>
            <h2>{{ t('composer.contentTitle') }}</h2>
          </div>
          <label class="app-form-grid">
            <span>{{ t('composer.textLabel') }}</span>
            <textarea
              v-model="content.text"
              rows="9"
              :placeholder="t('composer.textPlaceholder')"
            />
          </label>
          <label class="app-form-grid">
            <span>{{ t('composer.linkLabel') }}</span>
            <input
              v-model="content.link"
              type="url"
              inputmode="url"
              placeholder="https://"
            >
          </label>
        </article>

        <article class="app-card">
          <div class="app-card__header">
            <span class="app-card__eyebrow">{{ t('composer.mediaEyebrow') }}</span>
            <h2>{{ t('composer.mediaTitle') }}</h2>
          </div>
          <p>{{ t('composer.mediaDescription') }}</p>
          <label class="pq-button pq-button--secondary composer-file-button">
            <span>{{ uploading ? t('composer.uploading') : t('composer.upload') }}</span>
            <input
              type="file"
              accept="image/*,video/*"
              :disabled="uploading"
              @change="uploadMedia"
            >
          </label>
          <ul
            v-if="content.media.length"
            class="composer-media-list"
          >
            <li
              v-for="media in content.media"
              :key="media.id"
            >
              <div>
                <strong>{{ media.content_type }}</strong>
                <span>{{ Math.ceil(media.size_bytes / 1024) }} KB · {{ t(`composer.mediaStatus.${media.inspection_status}`) }}</span>
              </div>
              <button
                class="pq-button pq-button--secondary"
                type="button"
                @click="removeMedia(media)"
              >
                {{ t('composer.removeMedia') }}
              </button>
            </li>
          </ul>
        </article>

        <article class="app-card">
          <div class="app-card__header">
            <span class="app-card__eyebrow">{{ t('composer.destinationsEyebrow') }}</span>
            <h2>{{ t('composer.destinationsTitle') }}</h2>
          </div>
          <p>{{ t('composer.destinationsDescription') }}</p>
          <ul class="composer-destinations">
            <li
              v-for="connection in activeConnections"
              :key="connection.id"
            >
              <label class="composer-destination-toggle">
                <input
                  type="checkbox"
                  :checked="Boolean(selectedDestination(connection.id))"
                  :disabled="capabilitiesFor(connection).length === 0"
                  @change="toggleDestination(connection, ($event.target as HTMLInputElement).checked)"
                >
                <span>
                  <strong>{{ connection.display_name }}</strong>
                  <small>
                    {{ t(`social.provider.${connection.provider}`) }}
                    <template v-if="connection.handle"> · @{{ connection.handle }}</template>
                  </small>
                </span>
              </label>

              <div
                v-if="selectedDestination(connection.id)"
                class="composer-personalization"
              >
                <label>
                  <span>{{ t('composer.formatLabel') }}</span>
                  <select
                    :value="selectedDestination(connection.id)?.capability_id"
                    @change="changeCapability(connection, ($event.target as HTMLSelectElement).value)"
                  >
                    <option
                      v-for="capability in capabilitiesFor(connection)"
                      :key="capability.id"
                      :value="capability.id"
                    >
                      {{ t(`composer.format.${capability.format}`) }}
                    </option>
                  </select>
                </label>
                <fieldset
                  v-if="(selectedCapability(connection)?.fields?.length ?? 0) > 0"
                  class="composer-provider-fields"
                >
                  <legend>{{ t('composer.providerFieldsLegend') }}</legend>
                  <label
                    v-for="field in selectedCapability(connection)?.fields ?? []"
                    :key="field.name"
                  >
                    <span>
                      {{ field.name }}
                      <template v-if="field.required">{{ t('composer.requiredField') }}</template>
                    </span>
                    <select
                      v-if="field.allowed_values?.length"
                      :value="selectedDestination(connection.id)?.fields?.[field.name] ?? ''"
                      :required="field.required"
                      @change="updateProviderField(connection.id, field.name, ($event.target as HTMLSelectElement).value)"
                    >
                      <option value="">{{ t('composer.chooseFieldValue') }}</option>
                      <option
                        v-for="value in field.allowed_values"
                        :key="value"
                        :value="value"
                      >
                        {{ value }}
                      </option>
                    </select>
                    <input
                      v-else
                      type="text"
                      :value="selectedDestination(connection.id)?.fields?.[field.name] ?? ''"
                      :required="field.required"
                      :maxlength="field.max_length"
                      @input="updateProviderField(connection.id, field.name, ($event.target as HTMLInputElement).value)"
                    >
                    <small v-if="field.max_length">
                      {{ t('composer.maxCharacters', { count: field.max_length }) }}
                    </small>
                  </label>
                </fieldset>
                <label>
                  <span>{{ t('composer.personalizationLabel') }}</span>
                  <textarea
                    :value="selectedDestination(connection.id)?.text_override ?? ''"
                    rows="3"
                    :placeholder="t('composer.personalizationPlaceholder')"
                    @input="personalize(connection.id, ($event.target as HTMLTextAreaElement).value)"
                  />
                </label>
                <ul
                  v-if="validationErrors(selectedDestination(connection.id)?.id).length"
                  class="composer-validation"
                  role="alert"
                >
                  <li
                    v-for="error in validationErrors(selectedDestination(connection.id)?.id)"
                    :key="`${error.code}-${error.field}`"
                  >
                    <strong>{{ error.field }}:</strong> {{ error.message }}
                    <span v-if="error.remedy">{{ error.remedy }}</span>
                  </li>
                </ul>
              </div>
              <p
                v-else-if="capabilitiesFor(connection).length === 0"
                class="app-inline-note"
              >
                {{ t('composer.destinationUnavailable') }}
              </p>
            </li>
          </ul>
        </article>
      </div>

      <aside class="composer-sidebar">
        <article class="app-card composer-actions">
          <div class="app-card__header">
            <span class="app-card__eyebrow">{{ t('composer.actionsEyebrow') }}</span>
            <h2>{{ t('composer.actionsTitle') }}</h2>
          </div>
          <button
            class="pq-button pq-button--secondary"
            type="button"
            :disabled="saveState === 'saving'"
            @click="persist(false)"
          >
            {{ t('composer.saveDraft') }}
          </button>
          <button
            class="pq-button"
            type="button"
            :disabled="Boolean(action)"
            @click="submitSchedule(true)"
          >
            {{ action === 'publish' ? t('composer.publishing') : t('composer.publishNow') }}
          </button>
          <button
            class="pq-button pq-button--secondary"
            type="button"
            :aria-expanded="scheduleOpen"
            aria-controls="composer-schedule"
            @click="scheduleOpen = !scheduleOpen"
          >
            {{ t('composer.schedule') }}
          </button>
          <div
            v-if="scheduleOpen"
            id="composer-schedule"
            class="composer-schedule"
          >
            <label>
              <span>{{ t('composer.dateTimeLabel') }}</span>
              <input
                v-model="scheduleDateTime"
                type="datetime-local"
              >
            </label>
            <label>
              <span>{{ t('composer.timezoneLabel') }}</span>
              <select
                v-model="scheduleTimezone"
              >
                <option
                  v-for="zone in timezoneOptions"
                  :key="zone"
                  :value="zone"
                >
                  {{ zone }}
                </option>
              </select>
            </label>
            <label>
              <span>{{ t('composer.offsetLabel') }}</span>
              <input
                v-model="utcOffsetMinutes"
                type="number"
                min="-1080"
                max="1080"
                placeholder="+60"
              >
              <small>{{ t('composer.offsetHelp') }}</small>
            </label>
            <button
              class="pq-button"
              type="button"
              :disabled="Boolean(action)"
              @click="submitSchedule(false)"
            >
              {{ action === 'schedule' ? t('composer.scheduling') : t('composer.confirmSchedule') }}
            </button>
          </div>
        </article>
      </aside>
    </form>
  </section>
</template>
