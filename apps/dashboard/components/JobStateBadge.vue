<script setup lang="ts">
import type { JobResponse } from '~/utils/jobs'

/**
 * Come sta un job, in una parola.
 *
 * ## Perché gli stati sono cinque e non due
 *
 * `enabled: false` da solo non dice **chi ha premuto**, e all'utente che ritrova
 * venti job fermi la differenza è tutto ciò che conta (R58). Il backend tiene le
 * cose distinte apposta — la pausa dell'utente, la sospensione da cambio di
 * piano con il proprio motivo, l'archiviazione della riconciliazione — e
 * appiattirle qui in «attivo / non attivo» butterebbe via esattamente
 * l'informazione che serve a rimediare.
 *
 * I due motivi di sospensione restano distinti per la stessa ragione: portano a
 * due rimedi diversi, «scegli quali riaccendere» e «cambia la schedulazione»,
 * e un'etichetta unica costringerebbe a dire una delle due cose a tutti.
 */
const props = defineProps<{ job: JobResponse }>()

const { t } = useLocale()

type Tone = 'active' | 'paused' | 'warning' | 'muted'

const state = computed<{ label: string, tone: Tone }>(() => {
  const { state: labels } = t.value.jobs

  if (props.job.archived_at) return { label: labels.archived, tone: 'muted' }
  if (props.job.suspended?.reason === 'plan_resolution') {
    return { label: labels.suspendedByResolution, tone: 'warning' }
  }
  if (props.job.suspended) return { label: labels.suspendedByJobLimit, tone: 'warning' }
  if (!props.job.enabled) return { label: labels.paused, tone: 'paused' }
  return { label: labels.active, tone: 'active' }
})

/*
 * Il colore non è l'unico portatore dello stato: l'etichetta lo dice a parole
 * (R54, WCAG 2.2 1.4.1). Chi distingue male i colori legge comunque «In pausa».
 */
const TONES: Record<Tone, string> = {
  active: 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-300',
  paused: 'bg-gray-100 text-gray-800 dark:bg-gray-700 dark:text-gray-300',
  warning: 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-300',
  muted: 'bg-gray-100 text-gray-500 dark:bg-gray-700 dark:text-gray-400',
}
</script>

<template>
  <span class="inline-flex flex-wrap items-center gap-1">
    <span
      class="px-2 py-0.5 text-xs font-medium rounded"
      :class="TONES[state.tone]"
      data-testid="job-state"
    >{{ state.label }}</span>

    <!--
      «Viene da un repository» è ortogonale allo stato, non un sesto valore: un
      job sincronizzato può essere attivo, in pausa o sospeso come gli altri, e
      ciò che aggiunge è *dove si modifica* (R13).
    -->
    <span
      v-if="job.repository_id"
      class="px-2 py-0.5 text-xs font-medium text-blue-800 bg-blue-100 rounded dark:bg-blue-900 dark:text-blue-300"
      data-testid="job-managed"
    >{{ t.jobs.state.managed }}</span>
  </span>
</template>
