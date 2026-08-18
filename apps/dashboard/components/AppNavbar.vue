<script setup lang="ts">
/**
 * Barra superiore del template Flowbite: marchio a sinistra, comandi a destra,
 * e sotto i 1024 px il pulsante che apre la barra laterale.
 *
 * Markup di `layouts/partials/navbar-dashboard.html`, senza due cose che nel
 * template sono contenuto dimostrativo e qui sarebbero segnaposto in produzione
 * (SPEC R37): la ricerca globale (non c'è niente da cercare finché non ci sono
 * job) e le notifiche (R57, e vogliono un backend che le emetta). Il menu utente
 * c'è, in fondo alla barra come nel template, ma con l'utente vero: vedi
 * `AccountMenu.vue`.
 */
defineProps<{ navigationOpen: boolean }>()
const emit = defineEmits<{ toggleNavigation: [] }>()

// `href()` e non un percorso scritto a mano: le rotte sono prefissate per
// lingua (SPEC §8-bis), e il marchio deve riportare alla panoramica in quella
// che si sta leggendo.
const { t, href } = useLocale()
</script>

<template>
  <nav class="fixed z-30 w-full bg-white border-b border-gray-200 dark:bg-gray-800 dark:border-gray-700">
    <div class="px-3 py-3 lg:px-5 lg:pl-3">
      <div class="flex items-center justify-between">
        <div class="flex items-center justify-start">
          <!--
            `aria-controls` e `aria-expanded` dicono a chi non vede il pannello
            che questo pulsante lo governa e se è aperto (R54). Nel template
            `aria-expanded` è scritto fisso a `true` e non cambia mai: è un
            difetto, non una convenzione da copiare.
          -->
          <button
            type="button"
            class="p-2 text-gray-600 rounded cursor-pointer lg:hidden hover:text-gray-900 hover:bg-gray-100 focus:bg-gray-100 dark:focus:bg-gray-700 focus:ring-2 focus:ring-gray-100 dark:focus:ring-gray-700 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-white"
            aria-controls="sidebar"
            :aria-expanded="navigationOpen"
            :aria-label="navigationOpen ? t.shell.closeNavigation : t.shell.openNavigation"
            data-testid="navigation-toggle"
            @click="emit('toggleNavigation')"
          >
            <AppIcon
              :name="navigationOpen ? 'close' : 'menu'"
              class="w-6 h-6"
            />
          </button>

          <NuxtLink
            :to="href('/')"
            class="flex items-center ml-2 md:mr-24"
          >
            <span class="self-center text-xl font-semibold sm:text-2xl whitespace-nowrap dark:text-white">Postqron</span>
          </NuxtLink>
        </div>

        <div class="flex items-center gap-2">
          <LocaleSwitcher />
          <ThemeToggle />
          <AccountMenu />
        </div>
      </div>
    </div>
  </nav>
</template>
