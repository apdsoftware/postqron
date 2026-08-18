<script setup lang="ts">
import type { Occurrence, ScheduleSpec } from '~/utils/schedule'
import { nextOccurrences } from '~/utils/schedule'
import { fill } from '~/utils/text'

/**
 * Le prossime esecuzioni di una schedulazione, **nel fuso del job**.
 *
 * ## Perché è la parte che conta
 *
 * Un'espressione cron è difficile da leggere e facilissima da sbagliare:
 * `0 0 * * 0` e `0 0 0 * *` si somigliano e vogliono dire cose diversissime — la
 * prima ogni domenica a mezzanotte, la seconda mai, perché lo zero non è un
 * giorno del mese. Nessuna delle due dà un errore che si veda: la seconda dà un
 * job che non parte, e chi lo scopre lo scopre non vedendo succedere niente.
 * Questo riquadro è ciò che rende visibile la differenza mentre si scrive.
 *
 * ## Le tre cose che dichiara, e perché ciascuna
 *
 * 1. **Il fuso.** È quello del job (R1), non del browser. Un elenco di orari
 *    senza il fuso scritto accanto è più dannoso di nessun elenco, perché chi
 *    non vive in quel fuso lo leggerebbe come proprio.
 * 2. **L'ancoraggio dell'intervallo.** `every: 1h` scocca all'ora piena UTC, non
 *    un'ora dopo il salvataggio (SPEC §9). Senza questa riga l'anteprima
 *    sembrerebbe sbagliata proprio a chi ha capito bene, e con questa riga
 *    insegna una regola che altrimenti si scopre in produzione.
 * 3. **Le occorrenze che l'ora legale sposta.** Un orario che si muove di
 *    mezz'ora una volta l'anno, senza una spiegazione accanto, sembra un difetto
 *    del prodotto.
 *
 * ## Il valore autorevole resta quello del motore
 *
 * Quando il job esiste già, `next_run_at` è ciò che lo scheduler ha deciso e va
 * mostrato **accanto** a questo calcolo, non al suo posto: i due devono
 * coincidere, e vederli insieme è l'unico modo in cui una divergenza si nota.
 * Su un job appena creato `next_run_at` è `null` per costruzione (migrazione
 * 0010), ed è proprio il momento in cui questo riquadro è l'unica risposta
 * disponibile.
 */
const props = withDefaults(defineProps<{
  spec: ScheduleSpec
  /** Quante mostrarne. Cinque bastano a riconoscere una cadenza sbagliata. */
  count?: number
  /**
   * `next_run_at` del backend, quando il job esiste già: il valore autorevole.
   * `undefined` sul modulo di creazione, dove non esiste ancora.
   */
  scheduledAt?: string | null
}>(), { count: 5 })

const { t, locale } = useLocale()

/**
 * L'istante da cui si guarda avanti.
 *
 * Fisso invece che continuo, e si riaggiorna solo quando la schedulazione
 * cambia: un elenco che scorre da solo mentre si sta leggendo è rumore, e
 * l'unico momento in cui «adesso» conta davvero è quando si è appena scritta
 * un'espressione nuova.
 */
const from = ref(new Date())
watch(() => props.spec, () => {
  from.value = new Date()
}, { deep: true })

const result = computed(() => nextOccurrences(props.spec, from.value, props.count))

/** Le occorrenze, se la schedulazione si è potuta leggere. */
const occurrences = computed<Occurrence[]>(() => Array.isArray(result.value) ? result.value : [])

/** La schedulazione non si legge ancora: non c'è niente da mostrare. */
const unreadable = computed(() => !Array.isArray(result.value))

/** Si legge, ed è valida, ma non produce nessuna occorrenza: `0 0 30 2 *`. */
const never = computed(() => Array.isArray(result.value) && result.value.length === 0)

const timezone = computed(() => {
  const declared = (props.spec.timezone ?? '').trim()
  return declared === '' ? 'UTC' : declared
})

const isInterval = computed(() => (props.spec.everySeconds ?? 0) > 0)

/**
 * Gli orari li scrive `Intl`, che le cinque lingue le conosce già e non ci
 * costringe a tradurre i nomi dei mesi.
 *
 * `timeZoneName` non è decorativo: è la sigla del fuso *in quel momento
 * dell'anno* — CEST d'estate, CET d'inverno — ed è ciò che rende leggibile lo
 * scalino dell'ora legale invece di farlo sembrare un orario sbagliato.
 */
const formatter = computed(() => new Intl.DateTimeFormat(locale.value, {
  timeZone: timezone.value,
  // I campi si dichiarano uno per uno e non con `dateStyle`/`timeStyle`: quelle
  // due scorciatoie **non si possono combinare** con `timeZoneName`, e la
  // combinazione non dà una resa senza fuso — lancia un `TypeError`, cioè
  // toglie dallo schermo tutto il riquadro.
  weekday: 'short',
  day: 'numeric',
  month: 'short',
  year: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  timeZoneName: 'short',
}))

function format(occurrence: Occurrence): string {
  return formatter.value.format(occurrence.at)
}

/** La nota che spiega un'occorrenza spostata o ambigua; vuota per le normali. */
function note(occurrence: Occurrence): string {
  if (occurrence.resolution === 'gap') return t.value.jobs.preview.shifted
  if (occurrence.resolution === 'ambiguous') return t.value.jobs.preview.ambiguous
  return ''
}

/** Il valore autorevole del motore, quando c'è. */
const scheduled = computed(() => {
  if (props.scheduledAt === undefined || props.scheduledAt === null) return null
  return formatter.value.format(new Date(props.scheduledAt))
})
</script>

<template>
  <section
    class="p-4 border border-gray-200 rounded-lg bg-gray-50 dark:bg-gray-900 dark:border-gray-700"
    data-testid="schedule-preview"
  >
    <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
      {{ t.jobs.preview.title }}
    </h3>

    <!--
      Il fuso si dichiara sempre, anche quando l'elenco è vuoto: è
      l'informazione che rende leggibile tutto il resto del riquadro.
    -->
    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
      {{ fill(t.jobs.preview.inTimezone, { zone: timezone }) }}
    </p>
    <p
      v-if="isInterval"
      class="mt-1 text-xs text-gray-500 dark:text-gray-400"
      data-testid="preview-epoch"
    >
      {{ t.jobs.preview.epochAnchored }}
    </p>

    <p
      v-if="unreadable"
      class="mt-3 text-sm text-gray-500 dark:text-gray-400"
      data-testid="preview-invalid"
    >
      {{ t.jobs.preview.invalid }}
    </p>

    <!--
      «Non parte mai» è un esito, non un guasto, ed è il motivo principale per
      cui questo riquadro esiste: `0 0 30 2 *` è un job che nessuno vedrebbe non
      partire finché non serve. `role="status"` perché compare mentre si scrive.
    -->
    <p
      v-else-if="never"
      role="status"
      class="mt-3 text-sm text-amber-700 dark:text-amber-500"
      data-testid="preview-never"
    >
      {{ t.jobs.preview.never }}
    </p>

    <ol
      v-else
      class="mt-3 space-y-2"
      data-testid="preview-list"
    >
      <li
        v-for="(occurrence, index) in occurrences"
        :key="index"
        class="text-sm text-gray-900 dark:text-white"
      >
        <time :datetime="occurrence.at.toISOString()">{{ format(occurrence) }}</time>
        <span
          v-if="note(occurrence)"
          class="block text-xs text-amber-700 dark:text-amber-500"
          data-testid="preview-note"
        >{{ note(occurrence) }}</span>
      </li>
    </ol>

    <!--
      Il valore del motore accanto al nostro, non al suo posto: i due devono
      coincidere, e vederli insieme è l'unico modo in cui una divergenza si nota.
    -->
    <p
      v-if="scheduled"
      class="pt-3 mt-3 text-xs text-gray-500 border-t border-gray-200 dark:border-gray-700 dark:text-gray-400"
      data-testid="preview-scheduled"
    >
      {{ t.jobs.preview.scheduled }}: {{ scheduled }}
    </p>
  </section>
</template>
