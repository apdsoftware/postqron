<script setup lang="ts">
import { ApiError } from '~/utils/api'
import { authErrorKey, LOGIN_PATH, MIN_PASSWORD_LENGTH } from '~/utils/auth'

/**
 * Registrazione (R14).
 *
 * ## L'esito è vago apposta
 *
 * `POST /auth/register` risponde **202 identico byte per byte** che l'indirizzo
 * fosse libero o già registrato — è una proprietà che il backend verifica con un
 * test suo (`TestRegisterRispondeInModoIdenticoSuIndirizzoLiberoEPreso`), e
 * l'unico modo per l'interfaccia di rovinarla è dire più di quello che sa.
 *
 * Per questo la schermata di esito **non dice «account creato»**: dice che se
 * l'indirizzo è utilizzabile è partita un'email. Sembra un'esitazione, ed è la
 * differenza fra un modulo di registrazione e un servizio che dice a chiunque
 * quali indirizzi sono iscritti a Postqron. Non c'è nemmeno un accesso
 * automatico dopo la registrazione, e per lo stesso motivo prima ancora che per
 * il flusso: entrare vorrebbe dire che l'account è nostro, non entrare vorrebbe
 * dire che era di qualcun altro.
 *
 * Chi si è appena registrato davvero può accedere subito: la conferma
 * dell'indirizzo è un passo successivo e non blocca il login.
 */
definePageMeta({ layout: 'auth' })

const { t, href } = useLocale()
const session = useSession()

const fullName = ref('')
const email = ref('')
const password = ref('')
const pending = ref(false)
const accepted = ref(false)
const failure = ref<ReturnType<typeof authErrorKey> | null>(null)

async function submit(): Promise<void> {
  if (pending.value) return

  pending.value = true
  failure.value = null

  try {
    await session.register({ email: email.value, password: password.value, fullName: fullName.value })
    accepted.value = true
  }
  catch (cause) {
    if (!(cause instanceof ApiError)) throw cause

    failure.value = authErrorKey(cause)
  }
  finally {
    pending.value = false
  }
}

useHead(computed(() => ({ title: t.value.auth.signUp.title })))
</script>

<template>
  <div v-if="accepted">
    <h1 class="mb-3 text-xl font-bold text-gray-900 dark:text-white">
      {{ t.auth.signUp.acceptedTitle }}
    </h1>
    <p
      data-testid="register-accepted"
      class="text-sm font-light text-gray-500 dark:text-gray-400"
    >
      {{ t.auth.signUp.acceptedBody }}
    </p>
    <NuxtLink
      :to="href(LOGIN_PATH)"
      class="inline-block mt-6 text-sm font-medium text-primary-600 hover:underline dark:text-primary-500"
    >
      {{ t.auth.signUp.acceptedSignIn }}
    </NuxtLink>
  </div>

  <div v-else>
    <h1 class="mb-6 text-xl font-bold text-gray-900 dark:text-white">
      {{ t.auth.signUp.title }}
    </h1>

    <form
      class="space-y-4"
      data-testid="register-form"
      @submit.prevent="submit"
    >
      <AuthField
        v-model="fullName"
        type="text"
        autocomplete="name"
        autofocus
        :label="t.auth.fields.fullName"
      />
      <AuthField
        v-model="email"
        type="email"
        autocomplete="username"
        :label="t.auth.fields.email"
      />
      <!--
        Il requisito è scritto sotto la casella **prima** di scegliere la
        password, non mostrato come rifiuto dopo: scoprire la regola perdendo
        quello che si era scritto è il modo più sicuro di far scegliere la
        seconda password che viene in mente invece della migliore.
      -->
      <AuthField
        v-model="password"
        type="password"
        autocomplete="new-password"
        :minlength="MIN_PASSWORD_LENGTH"
        :label="t.auth.fields.password"
        :hint="t.auth.fields.passwordHint"
      />

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
        {{ pending ? t.auth.signUp.submitting : t.auth.signUp.submit }}
      </button>
    </form>

    <p class="mt-6 text-sm font-light text-gray-500 dark:text-gray-400">
      {{ t.auth.signUp.haveAccount }}
      <NuxtLink
        :to="href(LOGIN_PATH)"
        class="font-medium text-primary-600 hover:underline dark:text-primary-500"
      >
        {{ t.auth.signUp.haveAccountLink }}
      </NuxtLink>
    </p>
  </div>
</template>
