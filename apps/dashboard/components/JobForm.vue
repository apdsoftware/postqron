<script setup lang="ts">
import type { JobAlertChannel, JobEnvironment } from '~/utils/job-contract'
import type { JobDraft, JobIssue } from '~/utils/jobs'
import {
  JOB_ALERT_CHANNELS,
  JOB_BACKOFFS,
  JOB_ENVIRONMENTS,
  JOB_LIMITS,
  JOB_METHODS,
  JOB_OVERLAP_POLICIES,
} from '~/utils/job-contract'
import { draftSchedule } from '~/utils/jobs'

/**
 * Il modulo di un cronjob, condiviso da creazione e modifica.
 *
 * Uno solo per le due operazioni, come `JobPayload` è uno solo per `POST` e
 * `PATCH`: i campi sono gli stessi, e due moduli paralleli divergono al primo
 * campo aggiunto a uno solo dei due.
 *
 * ## Cosa fa e cosa non fa
 *
 * Non parla con il backend e non decide se salvare: riceve una bozza, la
 * modifica, e chiede a chi lo contiene di salvarla. I rilievi — i propri e
 * quelli che il server ha rimandato indietro — arrivano dall'esterno già
 * uniti, perché per un campo sono la stessa cosa: un motivo per cui non va.
 *
 * ## Il job che viene da un repository
 *
 * Un job sincronizzato da un `cron.yaml` non si modifica da qui: la
 * riconciliazione riporterebbe lo stato del file al primo push successivo, e la
 * modifica sparirebbe **senza un errore** (R13). L'unica eccezione è la pausa,
 * che il backend tiene deliberatamente distinta perché sopravviva al sync. Il
 * modulo lo dice in testa e disattiva il resto, invece di lasciar compilare e
 * rispondere 409: è la stessa regola di R15 applicata a un vincolo diverso.
 */
const draft = defineModel<JobDraft>({ required: true })

const props = withDefaults(defineProps<{
  /** I rilievi da mostrare accanto ai campi: i propri e quelli del server. */
  issues: readonly JobIssue[]
  /** Il job viene da un `cron.yaml`: solo la pausa è modificabile (R13). */
  managed?: boolean
  /** `next_run_at` del backend, per l'anteprima. */
  scheduledAt?: string | null
  pending?: boolean
}>(), { managed: false, pending: false })

const emit = defineEmits<{ submit: [] }>()

const { t } = useLocale()
const { messageForField } = useJobMessages()

const error = (field: Parameters<typeof messageForField>[1]): string =>
  messageForField(props.issues, field)

/** Tutto è bloccato tranne la pausa quando il job è del repository. */
const locked = computed(() => props.managed)

/**
 * L'elenco dei fusi lo dà il browser: sono quattrocento nomi che pesano zero nel
 * bundle, e sono gli stessi che `Intl` — e quindi l'anteprima — sa risolvere.
 *
 * Il fuso corrente entra sempre nell'elenco anche se non è fra quelli canonici:
 * un job nato da un `cron.yaml` può dichiarare un nome storico (`US/Pacific`),
 * che `time.LoadLocation` accetta e `supportedValuesOf` non elenca. Toglierlo
 * dalla tendina lo cancellerebbe dal job al primo salvataggio.
 */
const timezones = computed(() => {
  const supported = typeof Intl.supportedValuesOf === 'function'
    ? Intl.supportedValuesOf('timeZone')
    : []
  const current = draft.value.timezone.trim()
  return current !== '' && !supported.includes(current) ? [current, ...supported] : supported
})

/** La schedulazione che l'anteprima riceve, ricalcolata mentre si scrive. */
const schedule = computed(() => draftSchedule(draft.value))

function addHeader(): void {
  draft.value.headers.push({ name: '', value: '' })
}

function removeHeader(index: number): void {
  draft.value.headers.splice(index, 1)
}

function toggleEnvironment(env: JobEnvironment, on: boolean): void {
  draft.value.environments = on
    ? [...draft.value.environments, env]
    // L'ordine si mantiene quello del tipo enumerato, non quello dei click.
    : draft.value.environments.filter(value => value !== env)
}

function toggleChannel(channel: JobAlertChannel, on: boolean): void {
  draft.value.alertOnFailure = on
    ? [...draft.value.alertOnFailure, channel]
    : draft.value.alertOnFailure.filter(value => value !== channel)
}

const INPUT = 'block w-full p-2.5 text-sm text-gray-900 border rounded-lg bg-gray-50 focus:ring-primary-500 focus:border-primary-500 disabled:opacity-60 dark:bg-gray-700 dark:text-white dark:placeholder-gray-400'
const BORDER_OK = 'border-gray-300 dark:border-gray-600'
const BORDER_BAD = 'border-red-500 dark:border-red-500'

/** La classe di un controllo, col bordo che segue lo stato del campo. */
function control(invalid: boolean): string {
  return `${INPUT} ${invalid ? BORDER_BAD : BORDER_OK}`
}

const CHECKBOX = 'w-4 h-4 border border-gray-300 rounded bg-gray-50 focus:ring-2 focus:ring-primary-300 dark:bg-gray-700 dark:border-gray-600'
</script>

<template>
  <form
    class="grid gap-6 lg:grid-cols-3"
    data-testid="job-form"
    @submit.prevent="emit('submit')"
  >
    <div class="space-y-6 lg:col-span-2">
      <!--
        Il vincolo del repository si dice **prima**: compilare quindici campi e
        ricevere un 409 è lavoro buttato, e la sincronizzazione cancellerebbe la
        modifica senza nemmeno un errore.
      -->
      <div
        v-if="managed"
        role="status"
        class="p-4 text-sm text-blue-800 rounded-lg bg-blue-50 dark:bg-gray-800 dark:text-blue-300"
        data-testid="job-managed-notice"
      >
        <p class="font-medium">
          {{ t.jobs.form.managedTitle }}
        </p>
        <p class="mt-1">
          {{ t.jobs.form.managedBody }}
        </p>
      </div>

      <!-- ------------------------------------------------------- identità -->
      <fieldset class="p-4 bg-white border border-gray-200 rounded-lg shadow-sm dark:bg-gray-800 dark:border-gray-700">
        <legend class="px-1 text-sm font-semibold text-gray-900 dark:text-white">
          {{ t.jobs.form.sectionIdentity }}
        </legend>

        <div class="space-y-4">
          <JobFieldRow
            v-slot="{ id, describedBy, invalid }"
            :label="t.jobs.fields.name"
            :hint="t.jobs.fields.nameHint"
            :error="error('name')"
          >
            <input
              :id="id"
              v-model="draft.name"
              type="text"
              :disabled="locked"
              :aria-describedby="describedBy"
              :aria-invalid="invalid"
              :maxlength="JOB_LIMITS.maxNameLength"
              :class="control(invalid)"
              data-testid="job-name"
            >
          </JobFieldRow>

          <JobFieldRow
            v-slot="{ id, describedBy, invalid }"
            :label="t.jobs.fields.description"
            :error="error('description')"
            optional
          >
            <textarea
              :id="id"
              v-model="draft.description"
              rows="2"
              :disabled="locked"
              :aria-describedby="describedBy"
              :aria-invalid="invalid"
              :maxlength="JOB_LIMITS.maxDescriptionLength"
              :class="control(invalid)"
            />
          </JobFieldRow>
        </div>
      </fieldset>

      <!-- ---------------------------------------------------- schedulazione -->
      <fieldset class="p-4 bg-white border border-gray-200 rounded-lg shadow-sm dark:bg-gray-800 dark:border-gray-700">
        <legend class="px-1 text-sm font-semibold text-gray-900 dark:text-white">
          {{ t.jobs.form.sectionSchedule }}
        </legend>

        <div class="space-y-4">
          <!--
            Le due modalità sono mutuamente esclusive (SPEC §9), e un
            interruttore è l'unico modo di renderlo visibile: due campi affiancati
            inviterebbero a riempirli entrambi, che è un errore di validazione.
          -->
          <JobFieldRow
            :label="t.jobs.fields.mode"
            group
          >
            <div class="flex flex-wrap gap-4">
              <label
                v-for="mode in (['cron', 'interval'] as const)"
                :key="mode"
                class="inline-flex items-center gap-2 text-sm text-gray-900 dark:text-white"
              >
                <input
                  v-model="draft.mode"
                  type="radio"
                  :value="mode"
                  :disabled="locked"
                  class="w-4 h-4 border-gray-300 focus:ring-2 focus:ring-primary-300 dark:border-gray-600"
                  :data-testid="`job-mode-${mode}`"
                >
                {{ mode === 'cron' ? t.jobs.fields.modeCron : t.jobs.fields.modeInterval }}
              </label>
            </div>
          </JobFieldRow>

          <JobFieldRow
            v-if="draft.mode === 'cron'"
            v-slot="{ id, describedBy, invalid }"
            :label="t.jobs.fields.schedule"
            :hint="t.jobs.fields.scheduleHint"
            :error="error('schedule')"
          >
            <input
              :id="id"
              v-model="draft.schedule"
              type="text"
              spellcheck="false"
              autocapitalize="off"
              autocomplete="off"
              :disabled="locked"
              :aria-describedby="describedBy"
              :aria-invalid="invalid"
              :class="`${control(invalid)} font-mono`"
              data-testid="job-schedule"
            >
          </JobFieldRow>

          <JobFieldRow
            v-else
            v-slot="{ id, describedBy, invalid }"
            :label="t.jobs.fields.every"
            :error="error('every')"
          >
            <div class="flex gap-2">
              <input
                :id="id"
                v-model="draft.everyAmount"
                type="text"
                inputmode="numeric"
                :disabled="locked"
                :aria-describedby="describedBy"
                :aria-invalid="invalid"
                :class="control(invalid)"
                data-testid="job-every"
              >
              <select
                v-model="draft.everyUnit"
                :disabled="locked"
                :aria-label="t.jobs.fields.everyUnit"
                :class="control(false)"
                data-testid="job-every-unit"
              >
                <option
                  v-for="unit in (['s', 'm', 'h'] as const)"
                  :key="unit"
                  :value="unit"
                >
                  {{ t.jobs.options.everyUnits[unit] }}
                </option>
              </select>
            </div>
          </JobFieldRow>

          <JobFieldRow
            v-slot="{ id, describedBy, invalid }"
            :label="t.jobs.fields.timezone"
            :hint="t.jobs.fields.timezoneHint"
            :error="error('timezone')"
          >
            <select
              :id="id"
              v-model="draft.timezone"
              :disabled="locked"
              :aria-describedby="describedBy"
              :aria-invalid="invalid"
              :class="control(invalid)"
              data-testid="job-timezone"
            >
              <!-- I nomi IANA non si traducono: `Europe/Rome` è un identificatore. -->
              <option
                v-for="zone in timezones"
                :key="zone"
                :value="zone"
              >
                {{ zone }}
              </option>
            </select>
          </JobFieldRow>

          <JobFieldRow
            :label="t.jobs.fields.environments"
            :error="error('environments')"
            group
          >
            <div class="flex flex-wrap gap-4">
              <label
                v-for="env in JOB_ENVIRONMENTS"
                :key="env"
                class="inline-flex items-center gap-2 text-sm text-gray-900 dark:text-white"
              >
                <input
                  type="checkbox"
                  :checked="draft.environments.includes(env)"
                  :disabled="locked"
                  :class="CHECKBOX"
                  :data-testid="`job-env-${env}`"
                  @change="toggleEnvironment(env, ($event.target as HTMLInputElement).checked)"
                >
                {{ t.jobs.options.environments[env] }}
              </label>
            </div>
          </JobFieldRow>
        </div>
      </fieldset>

      <!-- ------------------------------------------------------- bersaglio -->
      <fieldset class="p-4 bg-white border border-gray-200 rounded-lg shadow-sm dark:bg-gray-800 dark:border-gray-700">
        <legend class="px-1 text-sm font-semibold text-gray-900 dark:text-white">
          {{ t.jobs.form.sectionTarget }}
        </legend>

        <div class="space-y-4">
          <JobFieldRow
            v-slot="{ id, describedBy, invalid }"
            :label="t.jobs.fields.url"
            :error="error('request.url')"
          >
            <input
              :id="id"
              v-model="draft.url"
              type="url"
              :disabled="locked"
              :aria-describedby="describedBy"
              :aria-invalid="invalid"
              :maxlength="JOB_LIMITS.maxUrlLength"
              :class="control(invalid)"
              data-testid="job-url"
            >
          </JobFieldRow>

          <JobFieldRow
            v-slot="{ id, describedBy, invalid }"
            :label="t.jobs.fields.method"
            :error="error('request.method')"
          >
            <select
              :id="id"
              v-model="draft.method"
              :disabled="locked"
              :aria-describedby="describedBy"
              :aria-invalid="invalid"
              :class="control(invalid)"
              data-testid="job-method"
            >
              <!--
                Se il job porta un metodo che questo bundle non conosce — un
                backend più nuovo — resta fra le opzioni invece di sparire: una
                tendina che non contiene il valore corrente lo cambia da sola.
              -->
              <option
                v-for="method in (JOB_METHODS.includes(draft.method) ? JOB_METHODS : [draft.method, ...JOB_METHODS])"
                :key="method"
                :value="method"
              >
                {{ method }}
              </option>
            </select>
          </JobFieldRow>

          <JobFieldRow
            :label="t.jobs.fields.headers"
            :error="error('request.headers')"
            group
            optional
          >
            <div class="space-y-2">
              <div
                v-for="(header, index) in draft.headers"
                :key="index"
                class="flex gap-2"
              >
                <input
                  v-model="header.name"
                  type="text"
                  spellcheck="false"
                  :disabled="locked"
                  :aria-label="t.jobs.fields.headerName"
                  :maxlength="JOB_LIMITS.maxHeaderNameLength"
                  :class="control(false)"
                  data-testid="header-name"
                >
                <input
                  v-model="header.value"
                  type="text"
                  spellcheck="false"
                  :disabled="locked"
                  :aria-label="t.jobs.fields.headerValue"
                  :class="control(false)"
                  data-testid="header-value"
                >
                <button
                  type="button"
                  :disabled="locked"
                  :aria-label="t.jobs.fields.removeHeader"
                  class="p-2 text-gray-500 rounded-lg hover:bg-gray-100 hover:text-gray-900 disabled:opacity-60 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-white"
                  data-testid="header-remove"
                  @click="removeHeader(index)"
                >
                  <AppIcon
                    name="close"
                    class="w-4 h-4"
                  />
                </button>
              </div>

              <button
                type="button"
                :disabled="locked"
                class="inline-flex items-center gap-1 text-sm font-medium text-primary-700 hover:underline disabled:opacity-60 dark:text-primary-400"
                data-testid="header-add"
                @click="addHeader()"
              >
                <AppIcon
                  name="plus"
                  class="w-4 h-4"
                />
                {{ t.jobs.fields.addHeader }}
              </button>
            </div>
          </JobFieldRow>

          <JobFieldRow
            v-slot="{ id, describedBy, invalid }"
            :label="t.jobs.fields.body"
            :error="error('request.body')"
            optional
          >
            <textarea
              :id="id"
              v-model="draft.body"
              rows="3"
              spellcheck="false"
              :disabled="locked"
              :aria-describedby="describedBy"
              :aria-invalid="invalid"
              :class="`${control(invalid)} font-mono`"
              data-testid="job-body"
            />
          </JobFieldRow>
        </div>
      </fieldset>

      <!-- ------------------------------------------------------ esecuzione -->
      <fieldset class="p-4 bg-white border border-gray-200 rounded-lg shadow-sm dark:bg-gray-800 dark:border-gray-700">
        <legend class="px-1 text-sm font-semibold text-gray-900 dark:text-white">
          {{ t.jobs.form.sectionExecution }}
        </legend>

        <div class="grid gap-4 sm:grid-cols-2">
          <JobFieldRow
            v-slot="{ id, describedBy, invalid }"
            :label="t.jobs.fields.timeout"
            :hint="t.jobs.fields.timeoutHint"
            :error="error('timeout')"
          >
            <input
              :id="id"
              v-model="draft.timeoutSeconds"
              type="text"
              inputmode="numeric"
              :disabled="locked"
              :aria-describedby="describedBy"
              :aria-invalid="invalid"
              :class="control(invalid)"
              data-testid="job-timeout"
            >
          </JobFieldRow>

          <JobFieldRow
            v-slot="{ id, describedBy, invalid }"
            :label="t.jobs.fields.retries"
            :error="error('retries.max')"
          >
            <input
              :id="id"
              v-model="draft.maxRetries"
              type="text"
              inputmode="numeric"
              :disabled="locked"
              :aria-describedby="describedBy"
              :aria-invalid="invalid"
              :class="control(invalid)"
              data-testid="job-retries"
            >
          </JobFieldRow>

          <JobFieldRow
            v-slot="{ id, describedBy, invalid }"
            :label="t.jobs.fields.backoff"
            :error="error('retries.backoff')"
          >
            <select
              :id="id"
              v-model="draft.retryBackoff"
              :disabled="locked"
              :aria-describedby="describedBy"
              :aria-invalid="invalid"
              :class="control(invalid)"
              data-testid="job-backoff"
            >
              <option
                v-for="backoff in JOB_BACKOFFS"
                :key="backoff"
                :value="backoff"
              >
                {{ t.jobs.options.backoff[backoff] }}
              </option>
            </select>
          </JobFieldRow>

          <!--
            R41. La conseguenza di ciascuna scelta è scritta sotto la tendina e
            cambia con la scelta: con la risoluzione al secondo la
            sovrapposizione non è un caso raro, è la norma, e `allow` su un
            bersaglio che fattura per chiamata fattura due volte.
          -->
          <JobFieldRow
            v-slot="{ id, describedBy, invalid }"
            :label="t.jobs.fields.overlap"
            :hint="t.jobs.options.overlapHint[draft.overlapPolicy] ?? t.jobs.fields.overlapHint"
            :error="error('on_overlap')"
          >
            <select
              :id="id"
              v-model="draft.overlapPolicy"
              :disabled="locked"
              :aria-describedby="describedBy"
              :aria-invalid="invalid"
              :class="control(invalid)"
              data-testid="job-overlap"
            >
              <option
                v-for="policy in JOB_OVERLAP_POLICIES"
                :key="policy"
                :value="policy"
              >
                {{ t.jobs.options.overlap[policy] }}
              </option>
            </select>
          </JobFieldRow>
        </div>
      </fieldset>

      <!-- --------------------------------------------------------- avvisi -->
      <fieldset class="p-4 bg-white border border-gray-200 rounded-lg shadow-sm dark:bg-gray-800 dark:border-gray-700">
        <legend class="px-1 text-sm font-semibold text-gray-900 dark:text-white">
          {{ t.jobs.form.sectionAlerts }}
        </legend>

        <div class="space-y-4">
          <JobFieldRow
            :label="t.jobs.fields.alerts"
            :error="error('alerts.on_failure')"
            group
          >
            <div class="flex flex-wrap gap-4">
              <label
                v-for="channel in JOB_ALERT_CHANNELS"
                :key="channel"
                class="inline-flex items-center gap-2 text-sm text-gray-900 dark:text-white"
              >
                <input
                  type="checkbox"
                  :checked="draft.alertOnFailure.includes(channel)"
                  :disabled="locked"
                  :class="CHECKBOX"
                  :data-testid="`job-alert-${channel}`"
                  @change="toggleChannel(channel, ($event.target as HTMLInputElement).checked)"
                >
                {{ t.jobs.options.alerts[channel] }}
              </label>
            </div>
          </JobFieldRow>

          <!--
            La pausa resta modificabile anche su un job del repository: la
            migrazione 0005 tiene `enabled` distinto da `archived_at` proprio
            perché sopravviva alla riconciliazione.
          -->
          <JobFieldRow
            :label="t.jobs.fields.enabled"
            :hint="t.jobs.fields.enabledHint"
            group
          >
            <label class="inline-flex items-center gap-2 text-sm text-gray-900 dark:text-white">
              <input
                v-model="draft.enabled"
                type="checkbox"
                :class="CHECKBOX"
                data-testid="job-enabled"
              >
              {{ t.jobs.fields.enabled }}
            </label>
          </JobFieldRow>
        </div>
      </fieldset>
    </div>

    <!--
      L'anteprima sta accanto ai campi e non sotto il pulsante: serve mentre si
      scrive l'espressione, non dopo aver salvato. `sticky` la tiene in vista
      scorrendo il resto del modulo.
    -->
    <div class="lg:col-span-1">
      <div class="space-y-4 lg:sticky lg:top-20">
        <SchedulePreview
          :spec="schedule"
          :scheduled-at="scheduledAt"
        />

        <slot name="aside" />

        <div class="flex flex-wrap gap-2">
          <button
            type="submit"
            :disabled="pending"
            class="px-5 py-2.5 text-sm font-medium text-white rounded-lg bg-primary-700 hover:bg-primary-800 focus:ring-4 focus:ring-primary-300 disabled:opacity-60 dark:bg-primary-600 dark:hover:bg-primary-700"
            data-testid="job-submit"
          >
            {{ pending ? t.jobs.form.saving : t.jobs.form.save }}
          </button>
          <slot name="actions" />
        </div>
      </div>
    </div>
  </form>
</template>
