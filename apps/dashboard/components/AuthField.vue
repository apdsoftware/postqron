<script setup lang="ts">
/**
 * Un campo dei moduli di autenticazione: etichetta, casella, ed eventuale
 * requisito scritto sotto.
 *
 * Esiste per una sola ragione, ed è l'etichetta. Un `<input>` senza `<label for>`
 * non ha nome accessibile: chi usa un lettore di schermo sente «casella di
 * testo» e chi usa il puntatore non può fare centro sull'etichetta per portarci
 * il fuoco. È la cosa che si dimentica per prima quando si scrive il markup di
 * un modulo a mano, e ci sono due moduli con cinque campi in tutto: qui
 * l'associazione è fatta una volta, con un identificativo generato da Vue e
 * quindi unico anche se lo stesso campo comparisse due volte nella stessa
 * schermata.
 *
 * `describedby` lega il requisito della password alla casella: senza, quella
 * riga è testo che sta lì vicino, e chi non vede la pagina non la sente mai.
 *
 * La validazione di base — obbligatorietà, forma dell'indirizzo — è quella del
 * browser (`required`, `type="email"`): è tradotta in tutte le lingue senza che
 * dobbiamo tradurla noi, e non lascia partire una richiesta che il backend
 * rifiuterebbe. Non è una difesa, e non pretende di esserlo: la regola vera è
 * quella del backend, che risponde 400 e il cui rifiuto il modulo mostra.
 */
const model = defineModel<string>({ required: true })

defineProps<{
  label: string
  type: 'text' | 'email' | 'password'
  /** Valore di `autocomplete`: dice al gestore di password che campo è. */
  autocomplete: string
  /** Requisito mostrato sotto la casella, quando ce n'è uno. */
  hint?: string
  /** Lunghezza minima, per la verifica del browser. */
  minlength?: number
  /** Il fuoco entra qui all'apertura della schermata. */
  autofocus?: boolean
}>()

const id = useId()
const hintId = `${id}-hint`
</script>

<template>
  <div>
    <label
      :for="id"
      class="block mb-2 text-sm font-medium text-gray-900 dark:text-white"
    >{{ label }}</label>
    <input
      :id="id"
      v-model="model"
      :type="type"
      :autocomplete="autocomplete"
      :minlength="minlength"
      :autofocus="autofocus"
      :aria-describedby="hint ? hintId : undefined"
      required
      class="block w-full p-2.5 text-sm text-gray-900 border border-gray-300 rounded-lg bg-gray-50 focus:ring-primary-500 focus:border-primary-500 dark:bg-gray-700 dark:border-gray-600 dark:text-white dark:placeholder-gray-400 dark:focus:ring-primary-500 dark:focus:border-primary-500"
    >
    <p
      v-if="hint"
      :id="hintId"
      class="mt-2 text-xs font-normal text-gray-500 dark:text-gray-400"
    >
      {{ hint }}
    </p>
  </div>
</template>
