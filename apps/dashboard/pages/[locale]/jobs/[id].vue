<script setup lang="ts">
import type { JobDraft, JobIssue, JobResponse, SubscriptionResponse } from '~/utils/jobs'
import { ApiError } from '~/utils/api'
import {
  draftFromJob,
  emptyDraft,
  issuesFromServer,
  jobPayload,
  validateDraft,
} from '~/utils/jobs'
import { fill } from '~/utils/text'

/**
 * Modifica di un cronjob, con le azioni che si fanno su un job che esiste già:
 * eseguirlo adesso, metterlo in pausa, eliminarlo.
 *
 * ## Perché le azioni stanno qui e non solo nell'elenco
 *
 * Perché è qui che si arriva quando si è in dubbio. L'elenco serve a scorrere;
 * questa schermata serve a capire un job — e chi la apre per capire perché non è
 * partito ha bisogno, nello stesso posto, dell'anteprima dei prossimi orari e
 * del pulsante che ne fa partire uno subito.
 *
 * ## La bozza si costruisce dal job, una volta sola
 *
 * Non è un `computed` sul dato remoto: sarebbe riscritta a ogni rilettura, e una
 * rilettura avviene dopo ogni azione — l'utente si vedrebbe cancellare le
 * modifiche in corso premendo «esegui adesso». Si copia all'arrivo del job, e da
 * lì appartiene al modulo.
 */
const { t, href } = useLocale()
const route = useRoute()
const api = useApi()

const id = computed(() => String(route.params.id ?? ''))

const job = useApiResource(signal =>
  api.request<JobResponse>(`/jobs/${id.value}`, { signal }))

const plan = useApiResource(signal =>
  api.request<SubscriptionResponse>('/billing/subscription', { signal }))

const draft = ref<JobDraft>(emptyDraft())
const issues = ref<JobIssue[]>([])
const failure = ref<string | null>(null)
const saved = ref(false)
const queued = ref(false)
const pending = ref(false)
const confirming = ref(false)

/*
 * La bozza nasce quando il job arriva, e **non** viene riscritta dalle riletture
 * successive: `job.data` cambia dopo ogni azione, e sovrascrivere il modulo
 * cancellerebbe ciò che si sta scrivendo.
 */
watch(job.data, (value) => {
  if (value) draft.value = draftFromJob(value)
}, { immediate: true })

/** Un job che viene da un `cron.yaml` si modifica lì, non da qui (R13). */
const managed = computed(() => Boolean(job.data.value?.repository_id))

/**
 * Sospeso per risoluzione: non si riaccende finché non cambia la schedulazione,
 * anche se c'è posto (R58). Il modulo lo dice, invece di lasciar salvare e
 * rispondere 403.
 */
const resolutionBlocked = computed(() =>
  job.data.value?.suspended?.reason === 'plan_resolution' && !job.data.value.enabled)

async function submit(): Promise<void> {
  if (pending.value) return

  issues.value = validateDraft(draft.value)
  failure.value = null
  saved.value = false
  if (issues.value.length > 0) return

  pending.value = true
  try {
    const updated = await api.request<JobResponse>(`/jobs/${id.value}`, {
      method: 'PATCH',
      body: jobPayload(draft.value),
    })
    job.data.value = updated
    // La bozza si riallinea a ciò che il server ha scritto davvero: la
    // normalizzazione del backend — spazi nell'espressione, fuso vuoto che
    // diventa `UTC` — deve vedersi, altrimenti il modulo mostra un job diverso
    // da quello salvato.
    draft.value = draftFromJob(updated)
    saved.value = true
    await plan.refresh()
  }
  catch (cause) {
    if (!(cause instanceof ApiError)) throw cause
    apply(cause)
  }
  finally {
    pending.value = false
  }
}

async function act(action: () => Promise<void>): Promise<void> {
  if (pending.value) return

  pending.value = true
  failure.value = null
  saved.value = false
  queued.value = false

  try {
    await action()
  }
  catch (cause) {
    if (!(cause instanceof ApiError)) throw cause
    apply(cause)
  }
  finally {
    pending.value = false
  }
}

async function trigger(): Promise<void> {
  await act(async () => {
    // 202: la riga è registrata, la chiamata la farà il motore.
    await api.request(`/jobs/${id.value}/executions`, { method: 'POST' })
    queued.value = true
  })
}

async function remove(): Promise<void> {
  confirming.value = false
  await act(async () => {
    await api.request(`/jobs/${id.value}`, { method: 'DELETE' })
    await navigateTo(href('/jobs'), { replace: true })
  })
}

function apply(error: ApiError): void {
  issues.value = issuesFromServer(error.details)

  switch (error.code) {
    case 'validation_failed':
      failure.value = issues.value.length > 0 ? null : t.value.jobs.form.invalidTitle
      break
    case 'job_name_taken':
      issues.value = [{ field: 'name', code: 'nameTaken' }]
      failure.value = null
      break
    case 'plan_limit_jobs':
      failure.value = t.value.jobs.plan.limitJobs
      break
    case 'plan_limit_resolution':
      failure.value = fill(t.value.jobs.plan.limitResolution, {
        plan: plan.data.value?.plan_name ?? error.plan ?? '',
        value: plan.data.value?.min_interval ?? '',
      })
      break
    case 'plan_limit_environments':
      failure.value = t.value.jobs.plan.limitEnvironments
      break
    case 'job_managed_by_repository':
      failure.value = t.value.jobs.form.managedBody
      break
    default:
      failure.value = t.value.jobs.form.unexpected
  }
}

useHead(computed(() => ({
  title: job.data.value?.name ?? t.value.jobs.form.editTitle,
})))
</script>

<template>
  <div>
    <div class="mb-6">
      <NuxtLink
        :to="href('/jobs')"
        class="inline-flex items-center gap-1 mb-2 text-sm font-medium text-primary-700 hover:underline dark:text-primary-400"
        data-testid="job-back"
      >
        <AppIcon
          name="back"
          class="w-4 h-4"
        />
        {{ t.jobs.form.back }}
      </NuxtLink>
      <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">
        {{ job.data.value?.name ?? t.jobs.form.editTitle }}
      </h1>
    </div>

    <AsyncState
      :pending="job.pending.value"
      :error="job.error.value"
      @retry="job.refresh()"
    >
      <div v-if="job.data.value">
        <!--
          R58: un job sospeso per risoluzione non si riaccende finché non cambia
          la schedulazione, e va detto in cima — non scoperto premendo «salva».
        -->
        <p
          v-if="resolutionBlocked"
          role="status"
          class="p-3 mb-4 text-sm rounded-lg bg-amber-50 text-amber-900 dark:bg-gray-800 dark:text-amber-300"
          data-testid="job-resolution-blocked"
        >
          {{ fill(t.jobs.plan.resolutionBlocked, { value: plan.data.value?.min_interval ?? '' }) }}
        </p>

        <p
          v-if="failure"
          role="alert"
          class="p-3 mb-4 text-sm text-red-800 rounded-lg bg-red-50 dark:bg-gray-800 dark:text-red-400"
          data-testid="job-form-error"
        >
          {{ failure }}
        </p>
        <p
          v-else-if="issues.length > 0"
          role="alert"
          class="p-3 mb-4 text-sm text-red-800 rounded-lg bg-red-50 dark:bg-gray-800 dark:text-red-400"
          data-testid="job-form-invalid"
        >
          {{ t.jobs.form.invalidTitle }}
        </p>
        <p
          v-else-if="queued"
          role="status"
          class="p-3 mb-4 text-sm text-green-800 rounded-lg bg-green-50 dark:bg-gray-800 dark:text-green-400"
          data-testid="job-run-queued"
        >
          {{ t.jobs.list.runQueued }}
        </p>

        <JobForm
          v-model="draft"
          :issues="issues"
          :managed="managed"
          :pending="pending"
          :scheduled-at="job.data.value.next_run_at"
          @submit="submit()"
        >
          <template #aside>
            <div class="flex flex-wrap items-center gap-2">
              <JobStateBadge :job="job.data.value" />
              <span
                v-if="saved"
                role="status"
                class="text-xs font-medium text-green-700 dark:text-green-400"
                data-testid="job-saved"
              >{{ t.jobs.form.saved }}</span>
            </div>
          </template>

          <template #actions>
            <button
              type="button"
              :disabled="pending || !job.data.value.enabled || Boolean(job.data.value.archived_at)"
              class="inline-flex items-center gap-1 px-4 py-2.5 text-sm font-medium text-gray-900 bg-white border border-gray-200 rounded-lg hover:bg-gray-100 disabled:opacity-60 dark:bg-gray-700 dark:text-white dark:border-gray-600 dark:hover:bg-gray-600"
              data-testid="job-run"
              @click="trigger()"
            >
              <AppIcon
                name="play"
                class="w-4 h-4"
              />
              {{ pending ? t.jobs.list.running : t.jobs.list.runNow }}
            </button>

            <button
              type="button"
              :disabled="pending"
              class="inline-flex items-center gap-1 px-4 py-2.5 text-sm font-medium text-red-700 bg-white border border-red-200 rounded-lg hover:bg-red-50 disabled:opacity-60 dark:bg-gray-700 dark:text-red-400 dark:border-gray-600 dark:hover:bg-gray-600"
              data-testid="job-delete"
              @click="confirming = true"
            >
              <AppIcon
                name="trash"
                class="w-4 h-4"
              />
              {{ t.jobs.list.delete }}
            </button>
          </template>
        </JobForm>
      </div>
    </AsyncState>

    <div
      v-if="confirming && job.data.value"
      class="fixed inset-0 z-30 flex items-center justify-center p-4 bg-gray-900/50"
    >
      <div
        role="alertdialog"
        aria-modal="true"
        :aria-label="t.jobs.list.deleteTitle"
        class="w-full max-w-md p-6 bg-white rounded-lg shadow dark:bg-gray-800"
        data-testid="job-delete-dialog"
      >
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
          {{ t.jobs.list.deleteTitle }}
        </h2>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">
          {{ job.data.value.name }}
        </p>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">
          {{ t.jobs.list.deleteBody }}
        </p>
        <div class="flex justify-end gap-2 mt-6">
          <button
            type="button"
            class="px-4 py-2 text-sm font-medium text-gray-900 bg-white border border-gray-200 rounded-lg hover:bg-gray-100 dark:bg-gray-700 dark:text-white dark:border-gray-600 dark:hover:bg-gray-600"
            data-testid="job-delete-cancel"
            @click="confirming = false"
          >
            {{ t.jobs.list.deleteCancel }}
          </button>
          <button
            type="button"
            class="px-4 py-2 text-sm font-medium text-white bg-red-700 rounded-lg hover:bg-red-800 focus:ring-4 focus:ring-red-300 dark:bg-red-600 dark:hover:bg-red-700"
            data-testid="job-delete-confirm"
            @click="remove()"
          >
            {{ t.jobs.list.deleteConfirm }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
