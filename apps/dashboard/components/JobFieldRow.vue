<script setup lang="ts">
/**
 * Un campo del modulo dei job: etichetta, controllo, requisito e rifiuto.
 *
 * ## Perché non basta scrivere `<label>` accanto a `<input>`
 *
 * Il modulo dei job ha quindici campi, ed è il triplo di quello di accesso. Le
 * tre cose che si dimenticano scrivendole a mano quindici volte sono sempre le
 * stesse, e sono tutte e tre invisibili a chi guarda lo schermo:
 *
 * 1. **`for` / `id`.** Un controllo senza etichetta associata non ha nome
 *    accessibile: chi usa un lettore di schermo sente «casella di testo», e chi
 *    usa il puntatore non può fare centro sull'etichetta per portarci il fuoco.
 * 2. **`aria-describedby`.** Il requisito scritto sotto — «il fuso è quello del
 *    job» — senza il collegamento è testo che sta lì vicino, e chi non vede la
 *    pagina non lo sente mai. Vale doppio per il messaggio di rifiuto.
 * 3. **`aria-invalid`.** Il bordo rosso è l'unico segnale che un campo è stato
 *    rifiutato, e il colore da solo non basta (R54, WCAG 2.2 1.4.1).
 *
 * Qui si fa una volta, e lo slot passa gli identificativi al controllo: un campo
 * nuovo li riceve, invece di doverseli ricordare.
 *
 * Il controllo arriva dallo slot invece di essere un `<input>` con dieci
 * proprietà, perché i quindici campi sono di sette tipi diversi — testo, numero,
 * elenco, caselle multiple, coppie nome/valore — e un componente che li coprisse
 * tutti sarebbe una tabella di condizioni più lunga del markup che sostituisce.
 */
defineProps<{
  label: string
  /** Requisito o spiegazione scritta sotto il controllo. */
  hint?: string
  /** Messaggio di rifiuto, già tradotto. Vuoto quando il campo va bene. */
  error?: string
  /** Marca il campo come facoltativo, accanto all'etichetta. */
  optional?: boolean
  /** Etichetta di un gruppo (caselle multiple) invece che di un singolo controllo. */
  group?: boolean
}>()

const { t } = useLocale()

const id = useId()
const hintId = `${id}-hint`
const errorId = `${id}-error`
</script>

<template>
  <div>
    <!--
      Un gruppo di caselle non ha un controllo solo a cui legare l'etichetta:
      lì `<label for>` punterebbe al nulla, e la forma corretta è un elemento di
      testo con `id`, che lo slot lega al gruppo con `aria-labelledby`.
    -->
    <component
      :is="group ? 'span' : 'label'"
      :id="group ? id : undefined"
      :for="group ? undefined : id"
      class="block mb-2 text-sm font-medium text-gray-900 dark:text-white"
    >
      {{ label }}<span
        v-if="optional"
        class="ml-1 font-normal text-gray-500 dark:text-gray-400"
      >{{ t.jobs.fields.optional }}</span>
    </component>

    <slot
      :id="id"
      :described-by="[hint ? hintId : '', error ? errorId : ''].filter(Boolean).join(' ') || undefined"
      :invalid="Boolean(error)"
    />

    <p
      v-if="hint"
      :id="hintId"
      class="mt-1 text-xs font-normal text-gray-500 dark:text-gray-400"
    >
      {{ hint }}
    </p>

    <!--
      `role="alert"` perché compare dopo un'azione — un invio, o l'uscita dal
      campo — e va annunciato subito a chi non lo vede comparire.
    -->
    <p
      v-if="error"
      :id="errorId"
      role="alert"
      class="mt-1 text-xs font-medium text-red-700 dark:text-red-400"
      data-testid="field-error"
    >
      {{ error }}
    </p>
  </div>
</template>
