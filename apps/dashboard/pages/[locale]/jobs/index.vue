<script setup lang="ts">
import type { JobListResponse, JobResponse, SubscriptionResponse } from '~/utils/jobs'
import { ApiError } from '~/utils/api'
import { fill } from '~/utils/text'

/**
 * L'elenco dei cronjob (SPEC §4.2).
 *
 * È l'indirizzo che le email transazionali compongono già — `AppURL("/jobs")`
 * in `emails/templates/plan_changed.*` — quindi ci si arriva anche da fuori, e
 * spesso proprio dopo un cambio di piano: la scheda del piano in cima non è
 * decorativa, è la risposta alla domanda con cui l'utente è arrivato.
 *
 * ## Le due letture, e perché sono due
 *
 * I job e il piano arrivano da rotte diverse e falliscono in modo indipendente.
 * Tenerli in due `useApiResource()` significa che un guasto della fatturazione
 * non nasconde l'elenco dei job — che è ciò per cui si è aperta la pagina — e
 * che ciascuno dichiara i propri quattro stati senza che l'altro debba saperlo
 * (R56).
 *
 * ## Le azioni scrivono, e poi rileggono
 *
 * Pausa, ripresa ed eliminazione cambiano lo stato lato server e poi ricaricano
 * entrambe le risorse invece di aggiornare la copia in memoria. Costa una
 * richiesta e toglie un'intera classe di difetti: il conteggio dei job attivi
 * del piano dipende da ciò che si è appena fatto, e due copie della stessa
 * verità aggiornate a mano divergono al primo caso che non si è previsto.
 */
const { t, href } = useLocale()
const api = useApi()

const jobs = useApiResource(signal => api.request<JobListResponse>('/jobs', {
  signal,
  // I job archiviati sono quelli spariti dal `cron.yaml` da cui venivano: non
  // sono cancellati, e nasconderli farebbe credere che lo siano (R13, R58).
  query: { include_archived: true, limit: 100 },
}))

const plan = useApiResource(signal =>
  api.request<SubscriptionResponse>('/billing/subscription', { signal }))

/** Il job su cui è aperta la conferma di eliminazione; `null` se nessuna. */
const confirming = ref<JobResponse | null>(null)
/** L'identificativo del job su cui una scrittura è in volo. */
const working = ref<string | null>(null)
/** Esito dell'ultimo trigger manuale, da annunciare. */
const queued = ref<string | null>(null)
/** Rifiuto dell'ultima azione, già tradotto. */
const failure = ref<string | null>(null)

/**
 * Esegue una scrittura e rilegge tutto.
 *
 * Il rifiuto si traduce **dal codice**, mai dal messaggio del backend, che è in
 * italiano (SPEC §8-bis). I due che meritano una frase propria sono i limiti di
 * piano: dicono che serve un piano diverso, e mescolarli con «riprova più
 * tardi» manderebbe a riprovare qualcosa che non cambierà.
 */
async function run(job: JobResponse, action: () => Promise<void>): Promise<void> {
  if (working.value !== null) return

  working.value = job.id
  failure.value = null
  queued.value = null

  try {
    await action()
    await Promise.all([jobs.refresh(), plan.refresh()])
  }
  catch (cause) {
    if (!(cause instanceof ApiError)) throw cause
    failure.value = actionFailure(cause)
  }
  finally {
    working.value = null
  }
}

function actionFailure(error: ApiError): string {
  if (error.code === 'plan_limit_jobs') return t.value.jobs.plan.limitJobs
  if (error.code === 'plan_limit_resolution') {
    return fill(t.value.jobs.plan.limitResolution, {
      plan: plan.data.value?.plan_name ?? error.plan ?? '',
      value: plan.data.value?.min_interval ?? '',
    })
  }
  return t.value.jobs.form.unexpected
}

async function toggle(job: JobResponse): Promise<void> {
  await run(job, async () => {
    await api.request(`/jobs/${job.id}`, { method: 'PATCH', body: { enabled: !job.enabled } })
  })
}

async function trigger(job: JobResponse): Promise<void> {
  await run(job, async () => {
    // 202: la riga è registrata, la chiamata la farà il motore. Dirlo con
    // «eseguito» prometterebbe un esito che non c'è ancora.
    await api.request(`/jobs/${job.id}/executions`, { method: 'POST' })
    queued.value = job.id
  })
}

async function remove(job: JobResponse): Promise<void> {
  confirming.value = null
  await run(job, async () => {
    await api.request(`/jobs/${job.id}`, { method: 'DELETE' })
  })
}

/**
 * Un job sospeso perché più fitto di quanto il piano consenta **non si
 * riaccende**, e il pulsante lo dice invece di lasciarlo premere (R58): non è
 * una questione di posto, e mettere in pausa un altro job non ne libera.
 */
function reactivationBlocked(job: JobResponse): boolean {
  return job.suspended?.reason === 'plan_resolution' && !job.enabled
}

function blockedReason(job: JobResponse): string {
  return reactivationBlocked(job)
    ? fill(t.value.jobs.plan.resolutionBlocked, { value: plan.data.value?.min_interval ?? '' })
    : ''
}

/** Il piano è pieno: si dice **prima** di aprire un modulo (R15). */
const full = computed(() => {
  const current = plan.data.value
  if (!current || current.max_jobs === undefined) return false
  return current.active_jobs >= current.max_jobs
})

/** La schedulazione, come si legge in una riga di tabella. */
function scheduleOf(job: JobResponse): string {
  return job.schedule ?? job.every ?? ''
}

useHead(computed(() => ({ title: t.value.jobs.list.title })))
</script>

<template>
  <div>
    <div class="flex flex-wrap items-start justify-between gap-4 mb-6">
      <div>
        <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">
          {{ t.jobs.list.title }}
        </h1>
        <p class="mt-1 text-base font-light text-gray-500 dark:text-gray-400">
          {{ t.jobs.list.intro }}
        </p>
      </div>

      <!--
        Quando il piano è pieno il comando resta visibile ma non porta al
        modulo, e dice perché. Nasconderlo lascerebbe cercare dove sia finito;
        lasciarlo attivo farebbe compilare quindici campi per un 403.
      -->
      <div class="text-right">
        <NuxtLink
          v-if="!full"
          :to="href('/jobs/new')"
          class="inline-flex items-center gap-1 px-4 py-2.5 text-sm font-medium text-white rounded-lg bg-primary-700 hover:bg-primary-800 focus:ring-4 focus:ring-primary-300 dark:bg-primary-600 dark:hover:bg-primary-700"
          data-testid="job-create"
        >
          <AppIcon
            name="plus"
            class="w-4 h-4"
          />
          {{ t.jobs.list.create }}
        </NuxtLink>
        <p
          v-else
          class="max-w-xs text-sm text-amber-700 dark:text-amber-500"
          data-testid="job-create-blocked"
        >
          {{ t.jobs.plan.jobsFull }}
        </p>
      </div>
    </div>

    <!--
      Il piano ha i propri quattro stati: un guasto della fatturazione non deve
      togliere dallo schermo l'elenco dei job.
    -->
    <div class="mb-6">
      <AsyncState
        :pending="plan.pending.value"
        :error="plan.error.value"
        @retry="plan.refresh()"
      >
        <JobPlanCard
          v-if="plan.data.value"
          :subscription="plan.data.value"
        />
      </AsyncState>
    </div>

    <p
      v-if="failure"
      role="alert"
      class="p-3 mb-4 text-sm text-red-800 rounded-lg bg-red-50 dark:bg-gray-800 dark:text-red-400"
      data-testid="jobs-action-error"
    >
      {{ failure }}
    </p>
    <p
      v-if="queued"
      role="status"
      class="p-3 mb-4 text-sm text-green-800 rounded-lg bg-green-50 dark:bg-gray-800 dark:text-green-400"
      data-testid="jobs-run-queued"
    >
      {{ t.jobs.list.runQueued }}
    </p>

    <AsyncState
      :pending="jobs.pending.value"
      :error="jobs.error.value"
      :empty="jobs.data.value?.jobs.length === 0"
      @retry="jobs.refresh()"
    >
      <template #empty>
        <p class="font-medium text-gray-900 dark:text-white">
          {{ t.jobs.list.empty }}
        </p>
        <p class="mt-1">
          {{ t.jobs.list.emptyHint }}
        </p>
      </template>

      <div
        v-if="jobs.data.value"
        class="overflow-x-auto bg-white border border-gray-200 rounded-lg shadow-sm dark:bg-gray-800 dark:border-gray-700"
      >
        <table class="w-full text-sm text-left text-gray-500 dark:text-gray-400">
          <thead class="text-xs text-gray-700 uppercase bg-gray-50 dark:bg-gray-700 dark:text-gray-400">
            <tr>
              <th
                scope="col"
                class="px-4 py-3"
              >
                {{ t.jobs.list.columnName }}
              </th>
              <th
                scope="col"
                class="px-4 py-3"
              >
                {{ t.jobs.list.columnSchedule }}
              </th>
              <th
                scope="col"
                class="px-4 py-3"
              >
                {{ t.jobs.list.columnNextRun }}
              </th>
              <th
                scope="col"
                class="px-4 py-3"
              >
                {{ t.jobs.list.columnState }}
              </th>
              <th
                scope="col"
                class="px-4 py-3 text-right"
              >
                {{ t.jobs.list.columnActions }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="job in jobs.data.value.jobs"
              :key="job.id"
              class="border-b dark:border-gray-700"
              data-testid="job-row"
            >
              <th
                scope="row"
                class="px-4 py-3 font-medium text-gray-900 whitespace-nowrap dark:text-white"
              >
                <NuxtLink
                  :to="href(`/jobs/${job.id}`)"
                  class="hover:underline"
                  data-testid="job-link"
                >{{ job.name }}</NuxtLink>
              </th>
              <td class="px-4 py-3 font-mono text-xs whitespace-nowrap">
                {{ scheduleOf(job) }}
                <!--
                  Il fuso accanto alla schedulazione, sempre: un'espressione cron
                  senza il fuso in cui va letta non dice a che ora parte (R1).
                -->
                <span class="block text-gray-400 dark:text-gray-500">{{ job.timezone }}</span>
              </td>
              <td class="px-4 py-3 whitespace-nowrap">
                <JobNextRun :job="job" />
              </td>
              <td class="px-4 py-3">
                <JobStateBadge :job="job" />
                <span
                  v-if="reactivationBlocked(job)"
                  class="block max-w-xs mt-1 text-xs text-amber-700 dark:text-amber-500"
                  data-testid="job-blocked"
                >{{ blockedReason(job) }}</span>
              </td>
              <td class="px-4 py-3">
                <div class="flex justify-end gap-1">
                  <button
                    type="button"
                    :disabled="working !== null || !job.enabled || Boolean(job.archived_at)"
                    :aria-label="t.jobs.list.runNow"
                    :title="t.jobs.list.runNow"
                    class="p-2 text-gray-500 rounded-lg hover:bg-gray-100 hover:text-gray-900 disabled:opacity-40 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-white"
                    data-testid="job-run"
                    @click="trigger(job)"
                  >
                    <AppIcon
                      name="play"
                      class="w-4 h-4"
                    />
                  </button>
                  <button
                    type="button"
                    :disabled="working !== null || reactivationBlocked(job) || Boolean(job.archived_at)"
                    :aria-label="job.enabled ? t.jobs.list.pause : t.jobs.list.resume"
                    :title="job.enabled ? t.jobs.list.pause : t.jobs.list.resume"
                    class="p-2 text-gray-500 rounded-lg hover:bg-gray-100 hover:text-gray-900 disabled:opacity-40 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-white"
                    data-testid="job-toggle"
                    @click="toggle(job)"
                  >
                    <AppIcon
                      :name="job.enabled ? 'pause' : 'play'"
                      class="w-4 h-4"
                    />
                  </button>
                  <NuxtLink
                    :to="href(`/jobs/${job.id}`)"
                    :aria-label="t.jobs.list.edit"
                    :title="t.jobs.list.edit"
                    class="p-2 text-gray-500 rounded-lg hover:bg-gray-100 hover:text-gray-900 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-white"
                    data-testid="job-edit"
                  >
                    <AppIcon
                      name="edit"
                      class="w-4 h-4"
                    />
                  </NuxtLink>
                  <button
                    type="button"
                    :disabled="working !== null"
                    :aria-label="t.jobs.list.delete"
                    :title="t.jobs.list.delete"
                    class="p-2 text-red-600 rounded-lg hover:bg-red-50 disabled:opacity-40 dark:text-red-400 dark:hover:bg-gray-700"
                    data-testid="job-delete"
                    @click="confirming = job"
                  >
                    <AppIcon
                      name="trash"
                      class="w-4 h-4"
                    />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </AsyncState>

    <!--
      L'eliminazione porta via anche lo storico delle esecuzioni, ed è
      irreversibile: una conferma esplicita è il minimo, e dice cosa sparisce
      invece di chiedere «sei sicuro?».
    -->
    <div
      v-if="confirming"
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
          {{ confirming.name }}
        </p>
        <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">
          {{ t.jobs.list.deleteBody }}
        </p>
        <div class="flex justify-end gap-2 mt-6">
          <button
            type="button"
            class="px-4 py-2 text-sm font-medium text-gray-900 bg-white border border-gray-200 rounded-lg hover:bg-gray-100 dark:bg-gray-700 dark:text-white dark:border-gray-600 dark:hover:bg-gray-600"
            data-testid="job-delete-cancel"
            @click="confirming = null"
          >
            {{ t.jobs.list.deleteCancel }}
          </button>
          <button
            type="button"
            class="px-4 py-2 text-sm font-medium text-white bg-red-700 rounded-lg hover:bg-red-800 focus:ring-4 focus:ring-red-300 dark:bg-red-600 dark:hover:bg-red-700"
            data-testid="job-delete-confirm"
            @click="remove(confirming)"
          >
            {{ t.jobs.list.deleteConfirm }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
