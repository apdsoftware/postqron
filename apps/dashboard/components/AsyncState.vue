<script setup lang="ts">
import type { ApiError } from '~/utils/api'

/**
 * I tre stati che ogni vista deve dichiarare (R56): in caricamento, in errore,
 * senza dati.
 *
 * ## Perché è un componente e non una convenzione
 *
 * «Ogni vista dichiara cosa mostra senza dati, in caricamento e quando la
 * richiesta fallisce» è una regola che, lasciata alla buona volontà, viene
 * rispettata dalle prime due viste e dimenticata dalla terza — e la vista che la
 * dimentica non sembra rotta: sembra vuota. Qui gli stati sono la forma di un
 * componente, e il caso normale (`<slot>`) è l'ultimo dei quattro rami: per
 * scriverlo bisogna passare dagli altri tre.
 *
 * ## Cosa **non** fa
 *
 * Non decide se ci sono dati. `empty` è una proprietà perché «vuoto» dipende dal
 * dato: zero job è vuoto, uno stato di salute senza campi è un guasto. Chi usa
 * il componente lo sa; questo componente no.
 *
 * Non traduce da sé i messaggi di errore del backend: prende la `kind` di
 * `ApiError` e la usa come chiave in `content.status.errors`. Il messaggio
 * dell'eccezione resta in console, dove serve a chi sviluppa, e non finisce
 * sullo schermo in inglese in mezzo a cinque lingue (SPEC §8-bis).
 */
const props = defineProps<{
  pending: boolean
  error: ApiError | null
  /** `true` quando la richiesta è riuscita ma non c'è niente da mostrare. */
  empty?: boolean
}>()

const emit = defineEmits<{ retry: [] }>()

const { t } = useLocale()

/**
 * «Riprova» compare solo dove riprovare può cambiare l'esito.
 *
 * Un 403 o un 404 danno lo stesso risultato all'infinito: offrire il pulsante
 * inviterebbe a premerlo, e a concludere che l'applicazione è rotta invece che
 * che la risposta è quella. Un guasto di rete e un 5xx sono transitori per
 * natura, e lì il pulsante è l'unica cosa da fare.
 */
const retryable = computed(
  () => props.error !== null && (props.error.kind === 'network' || props.error.kind === 'server'),
)

/**
 * Il caricamento sostituisce il contenuto solo quando non c'è ancora niente da
 * vedere. Un aggiornamento successivo — l'utente ha premuto «riprova» — lascia
 * in pagina il dato precedente: farlo sparire per riscriverlo uguale è uno
 * sfarfallio che fa perdere il segno a chi stava leggendo.
 */
const showSkeleton = computed(() => props.pending && props.error === null && !props.empty)
</script>

<template>
  <!--
    `aria-busy` e `aria-live` sul contenitore, non sul solo scheletro: chi usa un
    lettore di schermo deve sentire che è cambiato *qualcosa*, e il cambiamento
    riguarda tutta la regione, non il segnaposto che sparisce.
  -->
  <div
    :aria-busy="pending"
    aria-live="polite"
  >
    <div
      v-if="showSkeleton"
      role="status"
      data-testid="state-loading"
      class="animate-pulse"
    >
      <span class="sr-only">{{ t.status.loading }}</span>
      <div class="w-1/3 h-2.5 mb-4 bg-gray-200 rounded-full dark:bg-gray-700" />
      <div class="w-full h-2 mb-2.5 bg-gray-200 rounded-full dark:bg-gray-700" />
      <div class="w-5/6 h-2 mb-2.5 bg-gray-200 rounded-full dark:bg-gray-700" />
      <div class="w-2/3 h-2 bg-gray-200 rounded-full dark:bg-gray-700" />
    </div>

    <!--
      Riquadro d'errore nella forma degli alert di Flowbite. `role="alert"` lo fa
      annunciare appena compare: un errore che si può leggere solo scorrendo non
      è un errore dichiarato.
    -->
    <div
      v-else-if="error"
      role="alert"
      data-testid="state-error"
      class="p-4 text-sm text-red-800 rounded-lg bg-red-50 dark:bg-gray-800 dark:text-red-400"
    >
      <div class="flex items-center">
        <AppIcon
          name="alert"
          class="flex-shrink-0 w-5 h-5 me-2"
        />
        <span class="font-medium">{{ t.status.errorTitle }}</span>
      </div>
      <p class="mt-2">
        {{ t.status.errors[error.kind] }}
      </p>
      <button
        v-if="retryable"
        type="button"
        class="mt-3 text-white bg-red-700 hover:bg-red-800 focus:ring-4 focus:ring-red-300 font-medium rounded-lg text-xs px-3 py-1.5 text-center dark:bg-red-600 dark:hover:bg-red-700 dark:focus:ring-red-800"
        data-testid="state-retry"
        @click="emit('retry')"
      >
        {{ t.status.retry }}
      </button>
    </div>

    <div
      v-else-if="empty"
      data-testid="state-empty"
      class="py-8 text-sm text-center text-gray-500 dark:text-gray-400"
    >
      <!--
        Lo stato vuoto non ha un testo predefinito, e non è una dimenticanza:
        «nessun risultato» non aiuta nessuno. Cosa manca e cosa fare per averlo
        lo sa la vista — «nessun cronjob: creane uno» — e il messaggio arriva da
        lì attraverso questo slot.
      -->
      <slot name="empty" />
    </div>

    <slot v-else />
  </div>
</template>
