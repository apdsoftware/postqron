<script setup lang="ts">
/**
 * Guscio delle schermate raggiungibili senza sessione: accesso e registrazione.
 *
 * È un layout a sé e non il guscio di `default.vue` con dei pezzi nascosti,
 * perché barra laterale e navigazione fra le sezioni non hanno senso per chi non
 * è ancora entrato: sarebbero un elenco di posti dove non può andare. Il modo di
 * dichiararlo è quello previsto dal guscio principale, `definePageMeta({ layout:
 * 'auth' })`.
 *
 * Restano due comandi della barra superiore, e restano per ragioni diverse dalla
 * simmetria:
 *
 * - **Il selettore di lingua.** R32 lo vuole «nell'interfaccia», e questa è la
 *   prima schermata che si vede: se comparisse solo dopo l'accesso, chi non
 *   legge la lingua indovinata dal browser dovrebbe entrare al buio proprio nel
 *   momento in cui deve scrivere una password.
 * - **Il tema.** Chi ha scelto il tema scuro ce l'ha già applicato dallo script
 *   in testa al documento; senza l'interruttore, l'unica schermata da cui non lo
 *   si può cambiare sarebbe questa.
 */
const { t } = useLocale()
</script>

<template>
  <div class="flex flex-col min-h-screen bg-gray-50 dark:bg-gray-900">
    <a
      href="#main-content"
      class="sr-only focus:not-sr-only focus:absolute focus:z-40 focus:m-2 focus:rounded-lg focus:bg-primary-700 focus:px-4 focus:py-2 focus:text-sm focus:font-medium focus:text-white"
    >
      {{ t.shell.skipToContent }}
    </a>

    <header class="flex items-center justify-between px-4 py-3 sm:px-6">
      <span class="text-xl font-semibold text-gray-900 dark:text-white">Postqron</span>
      <div class="flex items-center gap-2">
        <LocaleSwitcher />
        <ThemeToggle />
      </div>
    </header>

    <main
      id="main-content"
      tabindex="-1"
      class="flex items-center justify-center flex-1 px-4 py-8 focus:outline-none"
    >
      <div class="w-full max-w-md p-6 bg-white border border-gray-200 rounded-lg shadow-sm sm:p-8 dark:bg-gray-800 dark:border-gray-700">
        <slot />
      </div>
    </main>
  </div>
</template>
