<script setup lang="ts">
import { ApiError } from '~/utils/api'
import { authErrorKey, REGISTER_PATH, safeNextPath } from '~/utils/auth'

/**
 * Accesso (R14).
 *
 * ## Cosa si dice, e cosa non si dice
 *
 * Il modulo ha **un solo messaggio di rifiuto** per le credenziali, e non
 * distingue fra indirizzo inesistente e password sbagliata. Il perché sta in
 * `authErrorKey()`, insieme al resto della corrispondenza: qui basti che è una
 * proprietà da non rompere, non un'omissione da colmare.
 *
 * ## I due modi di arrivare qui
 *
 * Ci si arriva scrivendo l'indirizzo, e allora non c'è altro da dire. Oppure ci
 * si arriva **rimbalzati**, e allora la schermata deve spiegare cosa è successo,
 * perché altrimenti l'unica lettura disponibile è che l'applicazione si è rotta:
 *
 * - la sessione è finita a metà lavoro — scadenza, o revoca da un altro
 *   dispositivo — e lo dice `session.interrupted`;
 * - si era chiesta una schermata protetta da scollegati, e lo dice `?next=`.
 *
 * In entrambi i casi il ritorno è a `next`, non alla panoramica. Il valore passa
 * da `safeNextPath()`, che è la stessa validazione che ha usato la guardia
 * mettendocelo: viaggia nella barra degli indirizzi, quindi da qui in poi è un
 * valore esterno come qualunque altro, anche se l'abbiamo scritto noi.
 */
definePageMeta({ layout: 'auth' })

const { t } = useLocale()
const session = useSession()
const route = useRoute()

const email = ref('')
const password = ref('')
const pending = ref(false)
const failure = ref<ReturnType<typeof authErrorKey> | null>(null)

/** Dove si tornerà dopo l'accesso. `null` quando non c'era un posto voluto. */
const next = computed(() => safeNextPath(route.query.next))

async function submit(): Promise<void> {
  // Il doppio invio non è ipotetico: il modulo si invia col tasto Invio, e una
  // rete lenta invita a premerlo di nuovo. Due login aprono due sessioni.
  if (pending.value) return

  pending.value = true
  failure.value = null

  try {
    await session.signIn(email.value, password.value)
    /*
     * `replace`: chi torna indietro dopo l'accesso deve trovare la pagina da cui
     * era partito, non il modulo che ha appena compilato — che la guardia
     * rimanderebbe subito qui, in un rimbalzo senza uscita apparente.
     */
    await navigateTo(next.value ?? '/', { replace: true })
  }
  catch (cause) {
    /*
     * Un errore che non è un `ApiError` è un difetto nostro: rilanciarlo lo fa
     * arrivare in console invece di travestirlo da credenziali sbagliate, che
     * manderebbe l'utente a riscrivere una password giusta all'infinito.
     */
    if (!(cause instanceof ApiError)) throw cause

    failure.value = authErrorKey(cause)
    // La password si azzera, l'indirizzo no: chi ha sbagliato a scriverla non
    // deve riscrivere anche l'email, e chi non ha sbagliato niente nemmeno.
    password.value = ''
  }
  finally {
    pending.value = false
  }
}

useHead(computed(() => ({ title: t.value.auth.signIn.title })))
</script>

<template>
  <div>
    <h1 class="mb-6 text-xl font-bold text-gray-900 dark:text-white">
      {{ t.auth.signIn.title }}
    </h1>

    <!--
      Perché si è qui. `role="status"` e non `role="alert"`: non è un errore e
      non ha sbagliato nessuno, è un fatto da annunciare senza interrompere.
    -->
    <p
      v-if="session.interrupted.value"
      role="status"
      data-testid="session-interrupted"
      class="p-3 mb-4 text-sm text-blue-800 rounded-lg bg-blue-50 dark:bg-gray-700 dark:text-blue-300"
    >
      {{ t.auth.signIn.interrupted }}
    </p>
    <p
      v-else-if="next"
      role="status"
      data-testid="session-returning"
      class="p-3 mb-4 text-sm text-gray-600 rounded-lg bg-gray-50 dark:bg-gray-700 dark:text-gray-300"
    >
      {{ t.auth.signIn.returningTo }}
    </p>

    <form
      class="space-y-4"
      data-testid="login-form"
      @submit.prevent="submit"
    >
      <AuthField
        v-model="email"
        type="email"
        autocomplete="username"
        autofocus
        :label="t.auth.fields.email"
      />
      <AuthField
        v-model="password"
        type="password"
        autocomplete="current-password"
        :label="t.auth.fields.password"
      />

      <!--
        Il rifiuto sta fra i campi e il pulsante, non in cima: è lì che guarda
        chi ha appena premuto. `role="alert"` perché compare dopo un'azione e va
        annunciato subito.
      -->
      <p
        v-if="failure"
        role="alert"
        data-testid="auth-error"
        class="p-3 text-sm text-red-800 rounded-lg bg-red-50 dark:bg-gray-700 dark:text-red-400"
      >
        {{ t.auth.errors[failure] }}
      </p>

      <button
        type="submit"
        :disabled="pending"
        data-testid="auth-submit"
        class="w-full px-5 py-2.5 text-sm font-medium text-center text-white rounded-lg bg-primary-700 hover:bg-primary-800 focus:ring-4 focus:ring-primary-300 disabled:opacity-60 dark:bg-primary-600 dark:hover:bg-primary-700 dark:focus:ring-primary-800"
      >
        {{ pending ? t.auth.signIn.submitting : t.auth.signIn.submit }}
      </button>
    </form>

    <p class="mt-6 text-sm font-light text-gray-500 dark:text-gray-400">
      {{ t.auth.signIn.noAccount }}
      <NuxtLink
        :to="REGISTER_PATH"
        class="font-medium text-primary-600 hover:underline dark:text-primary-500"
      >
        {{ t.auth.signIn.noAccountLink }}
      </NuxtLink>
    </p>
  </div>
</template>
