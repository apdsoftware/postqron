<script setup lang="ts">
import type { SubscriptionResponse } from '~/utils/jobs'
import { fill } from '~/utils/text'

/**
 * Il piano in forza con i suoi tre tetti, e i job che un cambio di piano ha
 * spento.
 *
 * ## Perché sta sopra l'elenco e non dentro un errore
 *
 * R15 chiede **due cose**: che i limiti siano applicati lato backend, e che
 * l'interfaccia li dica. La seconda non è una cortesia — un modulo con quindici
 * campi che si compila e poi viene rifiutato perché il piano è pieno è lavoro
 * buttato, e la spec lo dice per esteso a proposito della riattivazione: «va
 * prima cambiata la schedulazione. L'interfaccia deve dirlo, non limitarsi a
 * rifiutare».
 *
 * I numeri qui dentro **arrivano tutti da `GET /billing/subscription`**. Non c'è
 * nessuna tabella di listino nel client, e non deve essercene: i limiti vivono
 * in `plans`, e una seconda copia divergerebbe in silenzio dalla prima — cioè
 * l'interfaccia annuncerebbe un tetto e l'API ne farebbe rispettare un altro.
 *
 * ## I due motivi di sospensione, e perché non si sommano
 *
 * R58 li tiene distinti perché i rimedi sono due. Chi ha job fermi per il tetto
 * deve **sceglierne** alcuni da riaccendere, e la scelta è sua: due job identici
 * per schedulazione e destinazione possono valere uno la fatturazione mensile e
 * l'altro un promemoria, e nessun criterio automatico saprebbe quale. Chi ha job
 * fermi per la risoluzione non ha niente da scegliere: quel job, così com'è,
 * quel piano non lo esegue.
 */
defineProps<{ subscription: SubscriptionResponse }>()

const { t } = useLocale()
</script>

<template>
  <section
    class="p-4 bg-white border border-gray-200 rounded-lg shadow-sm dark:bg-gray-800 dark:border-gray-700"
    data-testid="plan-card"
  >
    <div class="flex flex-wrap items-baseline justify-between gap-2">
      <h2 class="text-sm font-semibold text-gray-900 dark:text-white">
        {{ t.jobs.plan.title }}
      </h2>
      <!-- Il nome commerciale non si traduce: «Pro» si chiama Pro ovunque. -->
      <span class="text-sm font-medium text-primary-700 dark:text-primary-400">{{ subscription.plan_name }}</span>
    </div>

    <!--
      I tre tetti di R15 uno accanto all'altro. Stanno insieme perché è insieme
      che descrivono cosa il piano permette di fare, e separarli costringerebbe
      a cercarli in tre posti prima di scrivere un job.
    -->
    <dl class="grid gap-2 mt-3 sm:grid-cols-3">
      <div>
        <dd
          class="text-sm text-gray-900 dark:text-white"
          data-testid="plan-jobs"
        >
          {{ subscription.max_jobs === undefined
            ? fill(t.jobs.plan.jobsUnlimited, { used: subscription.active_jobs })
            : fill(t.jobs.plan.jobsUsed, { used: subscription.active_jobs, limit: subscription.max_jobs }) }}
        </dd>
      </div>
      <div>
        <dd
          class="text-sm text-gray-900 dark:text-white"
          data-testid="plan-interval"
        >
          {{ fill(t.jobs.plan.minInterval, { value: subscription.min_interval }) }}
        </dd>
      </div>
      <div>
        <dd class="text-sm text-gray-900 dark:text-white">
          {{ fill(t.jobs.plan.retention, { days: subscription.log_retention_days }) }}
        </dd>
      </div>
    </dl>

    <!--
      R58. `role="status"` e non `role="alert"`: è successo prima che l'utente
      arrivasse qui, e va annunciato senza interrompere ciò che sta facendo.
    -->
    <div
      v-if="subscription.suspended_jobs.total > 0"
      role="status"
      class="p-3 mt-4 text-sm rounded-lg bg-amber-50 text-amber-900 dark:bg-gray-900 dark:text-amber-300"
      data-testid="plan-suspended"
    >
      <p class="font-medium">
        {{ t.jobs.plan.suspendedTitle }}
      </p>
      <p
        v-if="subscription.suspended_jobs.by_job_limit > 0"
        class="mt-1"
        data-testid="suspended-by-limit"
      >
        {{ fill(t.jobs.plan.suspendedByJobLimit, { count: subscription.suspended_jobs.by_job_limit }) }}
      </p>
      <p
        v-if="subscription.suspended_jobs.by_resolution > 0"
        class="mt-1"
        data-testid="suspended-by-resolution"
      >
        {{ fill(t.jobs.plan.suspendedByResolution, {
          count: subscription.suspended_jobs.by_resolution,
          value: subscription.min_interval,
        }) }}
      </p>
    </div>
  </section>
</template>
