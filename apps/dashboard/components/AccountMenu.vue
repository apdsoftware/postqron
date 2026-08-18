<script setup lang="ts">
/**
 * Menu dell'utente collegato, in fondo alla barra superiore: chi si è, e come
 * uscire.
 *
 * È il posto che la issue #24 aveva lasciato libero. Nel template Flowbite è una
 * tendina governata da `flowbite.js`; qui è stato di Vue, per la stessa ragione
 * per cui lo è il cassetto della barra laterale — quello script si aggancia agli
 * elementi una volta sola al caricamento, e questi si smontano e rimontano a
 * ogni cambio di rotta.
 *
 * L'indirizzo email si legge **dentro** il menu e non sul pulsante che lo apre:
 * a schermo stretto non ci starebbe, e una barra superiore in cui il proprio
 * indirizzo è sempre in chiaro è anche una barra superiore che lo mostra a
 * chiunque guardi lo schermo da dietro.
 */
const { t } = useLocale()
const session = useSession()
const route = useRoute()

const open = ref(false)
const signingOut = ref(false)
const root = ref<HTMLElement | null>(null)
const trigger = ref<HTMLButtonElement | null>(null)

/**
 * Esc chiude e **riporta il fuoco al pulsante** (R54, WCAG 2.2 2.1.2). Senza il
 * ritorno, chi naviga da tastiera chiude un pannello e si ritrova il fuoco sul
 * documento: il tabulatore successivo riparte dall'inizio della pagina.
 */
function onKeydown(event: KeyboardEvent) {
  if (event.key !== 'Escape' || !open.value) return

  open.value = false
  trigger.value?.focus()
}

/** Un clic fuori chiude: è ciò che ci si aspetta da una tendina. */
function onPointerDown(event: MouseEvent) {
  if (!open.value) return
  if (root.value?.contains(event.target as Node)) return

  open.value = false
}

onMounted(() => {
  document.addEventListener('keydown', onKeydown)
  document.addEventListener('mousedown', onPointerDown)
})
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKeydown)
  document.removeEventListener('mousedown', onPointerDown)
})

// Cambiare schermata chiude il menu: resterebbe aperto sopra una pagina diversa
// da quella su cui è stato aperto.
watch(() => route.fullPath, () => {
  open.value = false
})

async function signOut(): Promise<void> {
  if (signingOut.value) return

  signingOut.value = true
  try {
    await session.signOut()
  }
  finally {
    signingOut.value = false
  }
}
</script>

<template>
  <div
    v-if="session.user.value"
    ref="root"
    class="relative"
  >
    <button
      ref="trigger"
      type="button"
      class="flex items-center justify-center w-8 h-8 text-sm font-medium text-white rounded-full cursor-pointer bg-primary-700 focus:ring-4 focus:ring-gray-300 dark:focus:ring-gray-600"
      :aria-expanded="open"
      aria-haspopup="menu"
      :aria-label="t.shell.account.open"
      data-testid="account-toggle"
      @click="open = !open"
    >
      <!--
        L'iniziale dell'indirizzo al posto di una fotografia che non abbiamo. È
        decorativa — il nome del pulsante è `aria-label` — quindi nascosta a chi
        ascolta, che sentirebbe altrimenti una lettera senza contesto.
      -->
      <span aria-hidden="true">{{ session.user.value.email.slice(0, 1).toUpperCase() }}</span>
    </button>

    <div
      v-if="open"
      class="absolute right-0 z-40 w-56 mt-2 text-left bg-white border border-gray-200 rounded-lg shadow-lg dark:bg-gray-700 dark:border-gray-600"
      data-testid="account-menu"
    >
      <div class="px-4 py-3 border-b border-gray-200 dark:border-gray-600">
        <p class="text-xs text-gray-500 dark:text-gray-400">
          {{ t.shell.account.signedInAs }}
        </p>
        <p class="text-sm font-medium text-gray-900 truncate dark:text-white">
          {{ session.user.value.email }}
        </p>
      </div>
      <button
        type="button"
        class="block w-full px-4 py-2 text-sm text-left text-gray-700 cursor-pointer hover:bg-gray-100 disabled:opacity-60 dark:text-gray-200 dark:hover:bg-gray-600"
        :disabled="signingOut"
        data-testid="sign-out"
        @click="signOut"
      >
        {{ t.shell.account.signOut }}
      </button>
    </div>
  </div>
</template>
