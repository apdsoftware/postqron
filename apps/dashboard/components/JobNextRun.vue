<script setup lang="ts">
import type { JobResponse } from '~/utils/jobs'

/**
 * Quando parte la prossima volta, **nel fuso del job**.
 *
 * ## I tre casi, che sono davvero diversi
 *
 * - **Un istante.** È `next_run_at`, calcolato dallo scheduler: il valore
 *   autorevole, non una nostra stima.
 * - **Non ancora calcolato.** `next_run_at` è `null` su un job appena creato, e
 *   non è un guasto: la colonna è dello scheduler, e la migrazione 0010 dice per
 *   esteso che un job nato da API o dashboard ci cade dentro senza, finché il
 *   motore non lo raccoglie alla passata successiva. Un trattino al suo posto lo
 *   farebbe sembrare rotto proprio nei secondi che seguono la creazione, cioè
 *   nel momento più delicato del percorso al primo job (R55).
 * - **Nessuna.** Il job è in pausa, sospeso o archiviato: non ne ha una, e
 *   dirlo è diverso dal dire che non si sa.
 *
 * L'orario lo scrive `Intl`, con il fuso del job e la sua sigla accanto: senza,
 * chi non vive in quel fuso lo leggerebbe come proprio (R1).
 */
const props = defineProps<{ job: JobResponse }>()

const { t, locale } = useLocale()

const formatted = computed(() => {
  if (props.job.next_run_at === null) return null

  return new Intl.DateTimeFormat(locale.value, {
    timeZone: props.job.timezone || 'UTC',
    // Campo per campo, non `dateStyle`/`timeStyle`: quelle due non si possono
    // combinare con `timeZoneName`, e la combinazione lancia invece di
    // ripiegare. Vedi la stessa nota in `SchedulePreview.vue`.
    day: 'numeric',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
    timeZoneName: 'short',
  }).format(new Date(props.job.next_run_at))
})

/** Fermo: non ne ha una, che è diverso da «non si sa ancora». */
const stopped = computed(() =>
  !props.job.enabled || Boolean(props.job.archived_at))
</script>

<template>
  <time
    v-if="formatted && job.next_run_at"
    :datetime="job.next_run_at"
    class="text-gray-900 dark:text-white"
    data-testid="job-next-run"
  >{{ formatted }}</time>
  <span
    v-else-if="stopped"
    class="text-gray-400 dark:text-gray-500"
    data-testid="job-next-none"
  >{{ t.jobs.list.nextRunNone }}</span>
  <span
    v-else
    class="text-gray-400 dark:text-gray-500"
    data-testid="job-next-pending"
  >{{ t.jobs.list.nextRunPending }}</span>
</template>
