<script setup lang="ts">
import type { JobIssue, JobResponse, SubscriptionResponse } from '~/utils/jobs'
import { ApiError } from '~/utils/api'
import { emptyDraft, issuesFromServer, jobPayload, validateDraft } from '~/utils/jobs'
import { fill } from '~/utils/text'

/**
 * Creazione di un cronjob.
 *
 * È l'indirizzo del pulsante dell'email di benvenuto — `AppURL("/jobs/new")` in
 * `emails/templates/welcome.*` — quindi è letteralmente il primo passo del
 * percorso al primo job (R55): ci si arriva da un'email, spesso senza aver mai
 * visto il prodotto.
 *
 * ## Il tetto del piano si legge prima di compilare
 *
 * Il piano si carica insieme al modulo, e quando è pieno il modulo non si mostra
 * affatto: quindici campi compilati per un 403 `plan_limit_jobs` sono lavoro
 * buttato, ed è esattamente il caso che R15 chiede di evitare. Il rifiuto del
 * backend resta gestito, perché fra il caricamento e il salvataggio un'altra
 * scheda può aver creato l'ultimo job disponibile.
 */
const { t, href } = useLocale()
const api = useApi()

const draft = ref(emptyDraft())
const pending = ref(false)
/** I rilievi mostrati: i propri e quelli del server, che per un campo sono la
 * stessa cosa — un motivo per cui non va. */
const issues = ref<JobIssue[]>([])
/** Rifiuto complessivo, quando non appartiene a nessun campo. */
const failure = ref<string | null>(null)

const plan = useApiResource(signal =>
  api.request<SubscriptionResponse>('/billing/subscription', { signal }))

const full = computed(() => {
  const current = plan.data.value
  if (!current || current.max_jobs === undefined) return false
  return current.active_jobs >= current.max_jobs
})

async function submit(): Promise<void> {
  if (pending.value) return

  /*
   * La verifica del client è la prima parola, non l'ultima: se trova qualcosa
   * si ferma qui e non spende una richiesta, ma ciò che passa non è
   * «approvato» — è solo «non ovviamente sbagliato». Il giudice è il backend, e
   * il suo rifiuto arriva nello stesso elenco.
   */
  issues.value = validateDraft(draft.value)
  failure.value = null
  if (issues.value.length > 0) return

  pending.value = true
  try {
    const created = await api.request<JobResponse>('/jobs', {
      method: 'POST',
      body: jobPayload(draft.value),
    })
    /*
     * `replace`: chi torna indietro dopo aver creato un job deve trovare
     * l'elenco, non un modulo già inviato che un secondo invio duplicherebbe.
     */
    await navigateTo(href(`/jobs/${created.id}`), { replace: true })
  }
  catch (cause) {
    if (!(cause instanceof ApiError)) throw cause
    apply(cause)
  }
  finally {
    pending.value = false
  }
}

/**
 * Distribuisce un rifiuto del backend fra i campi e la testata.
 *
 * I messaggi del server non si mostrano mai: sono in italiano, e le lingue sono
 * cinque (SPEC §8-bis). Ciò che si usa è il **codice**, che è stabile e che il
 * backend dichiara essere il campo su cui un client decide (R53). I limiti di
 * piano hanno una frase propria perché portano a un rimedio diverso: non
 * «correggi», ma «serve un piano che lo consenta» — e i numeri dentro la frase
 * arrivano da `/billing/subscription`, non da una tabella scritta qui.
 */
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
      // Il numero può essere cambiato mentre si compilava: rileggerlo è ciò che
      // fa sparire il modulo invece di lasciarlo riprovare a vuoto.
      void plan.refresh()
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
    default:
      failure.value = t.value.jobs.form.unexpected
  }
}

useHead(computed(() => ({ title: t.value.jobs.form.createTitle })))
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
        {{ t.jobs.form.createTitle }}
      </h1>
    </div>

    <!--
      Il piano ha i propri stati: finché non si sa quanti job sono consentiti non
      si può nemmeno dire se il modulo va mostrato.
    -->
    <AsyncState
      :pending="plan.pending.value"
      :error="plan.error.value"
      @retry="plan.refresh()"
    >
      <div
        v-if="full"
        role="status"
        class="p-4 text-sm rounded-lg bg-amber-50 text-amber-900 dark:bg-gray-800 dark:text-amber-300"
        data-testid="job-create-blocked"
      >
        <p class="font-medium">
          {{ t.jobs.plan.jobsFull }}
        </p>
        <NuxtLink
          :to="href('/jobs')"
          class="inline-block mt-2 font-medium underline"
        >
          {{ t.jobs.form.back }}
        </NuxtLink>
      </div>

      <div v-else>
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

        <JobForm
          v-model="draft"
          :issues="issues"
          :pending="pending"
          @submit="submit()"
        >
          <template #aside>
            <JobPlanCard
              v-if="plan.data.value"
              :subscription="plan.data.value"
            />
          </template>
          <template #actions>
            <NuxtLink
              :to="href('/jobs')"
              class="px-5 py-2.5 text-sm font-medium text-gray-900 bg-white border border-gray-200 rounded-lg hover:bg-gray-100 dark:bg-gray-700 dark:text-white dark:border-gray-600 dark:hover:bg-gray-600"
              data-testid="job-cancel"
            >
              {{ t.jobs.form.cancel }}
            </NuxtLink>
          </template>
        </JobForm>
      </div>
    </AsyncState>
  </div>
</template>
