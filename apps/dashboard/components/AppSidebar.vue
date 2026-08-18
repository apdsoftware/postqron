<script setup lang="ts">
import { stripLocale } from '~/utils/locale'
import { isActivePath, NAVIGATION } from '~/utils/navigation'

/**
 * Barra laterale del template Flowbite: navigazione fissa da 1024 px in su,
 * cassetto sopra il contenuto sotto quella soglia.
 *
 * Il markup è quello di `layouts/partials/sidebar.html`. Cambiano due cose, ed
 * entrambe hanno un motivo:
 *
 * 1. **Le voci arrivano da `utils/navigation.ts`**, non sono scritte qui. È ciò
 *    che rende una sezione nuova una riga di registro invece di un blocco di
 *    markup da copiare, con il rischio che la copia perda una classe.
 * 2. **Apertura e chiusura sono stato di Vue**, non `classList.toggle()` come
 *    nel `sidebar.js` del template. Quello script si aggancia agli elementi una
 *    volta sola al caricamento; qui il markup si smonta e si rimonta a ogni
 *    cambio di rotta, e i suoi ascoltatori resterebbero appesi a nodi che non
 *    esistono più. È lo stesso motivo per cui non importiamo `flowbite.js` (il
 *    perché per esteso sta in `assets/css/theme.css` e nella PR).
 *
 * Il piede della barra — nel template un ingranaggio e una tendina delle lingue,
 * entrambi `hidden lg:flex` — qui non c'è. Il selettore di lingua deve stare
 * dove si vede su **ogni** schermo (R32: «nell'interfaccia»), e quel piede
 * scompare proprio sul telefono; sta quindi nella barra superiore. Le scorciatoie
 * alle impostazioni arriveranno con la issue che porta le impostazioni (#29).
 */
const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: [] }>()

const { t, href } = useLocale()
const route = useRoute()

/**
 * Dove si è, **senza lingua**: è la forma in cui il registro scrive i percorsi,
 * ed è ciò che rende l'evidenziazione della voce corrente indifferente alla
 * lingua senza che `isActivePath()` debba sapere che le lingue esistono.
 *
 * Si toglie qui, in un punto solo, e non a ogni confronto: una sezione nuova
 * (#26, #27, #28, #29) è una riga in `utils/navigation.ts`, e non deve poter
 * dimenticare il prefisso.
 */
const here = computed(() => stripLocale(route.path))

/**
 * Chi la usa da telefono ha aperto un cassetto sopra la pagina: toccare una
 * voce deve richiuderlo. Senza, la pagina cambia dietro un pannello che copre
 * lo schermo e sembra che non sia successo niente.
 */
watch(() => route.path, () => {
  if (props.open) emit('close')
})
</script>

<template>
  <aside
    id="sidebar"
    class="fixed top-0 left-0 z-20 flex-col flex-shrink-0 w-64 h-full pt-16 font-normal duration-75 lg:flex transition-width"
    :class="open ? 'flex' : 'hidden'"
    :aria-label="t.shell.navigationLabel"
  >
    <div class="relative flex flex-col flex-1 min-h-0 pt-0 bg-white border-r border-gray-200 dark:bg-gray-800 dark:border-gray-700">
      <div class="flex flex-col flex-1 pt-5 pb-4 overflow-y-auto">
        <div class="flex-1 px-3 space-y-1 bg-white divide-y divide-gray-200 dark:bg-gray-800 dark:divide-gray-700">
          <!--
            `<nav>` dentro `<aside>`: l'elemento di riferimento è la navigazione,
            ed è quello che i lettori di schermo elencano fra i punti di
            riferimento della pagina. Il nome accessibile sta sull'`<aside>`
            perché il cassetto è ciò che si apre e si chiude.
          -->
          <nav>
            <ul class="pb-2 space-y-2">
              <li
                v-for="entry in NAVIGATION"
                :key="entry.id"
              >
                <!--
                  `aria-current="page"` e non il solo colore: l'evidenziazione
                  della voce corrente deve arrivare anche a chi non vede lo
                  sfondo grigio (R54, WCAG 2.2 1.4.1).
                -->
                <NuxtLink
                  :to="href(entry.path)"
                  class="flex items-center p-2 text-base text-gray-900 rounded-lg hover:bg-gray-100 group dark:text-gray-200 dark:hover:bg-gray-700"
                  :class="isActivePath(entry.path, here) ? 'bg-gray-100 dark:bg-gray-700' : ''"
                  :aria-current="isActivePath(entry.path, here) ? 'page' : undefined"
                >
                  <AppIcon
                    :name="entry.icon"
                    class="w-6 h-6 text-gray-500 transition duration-75 group-hover:text-gray-900 dark:text-gray-400 dark:group-hover:text-white"
                  />
                  <span class="ml-3">{{ t.shell.nav[entry.id] }}</span>
                </NuxtLink>
              </li>
            </ul>
          </nav>
        </div>
      </div>
    </div>
  </aside>

  <!--
    Velo sotto il cassetto. È un `<div>` e non un `<button>` perché non è un
    comando da raggiungere con il tabulatore: chi naviga da tastiera chiude con
    Esc, e chi usa il puntatore tocca fuori dal pannello.
  -->
  <div
    v-if="open"
    class="fixed inset-0 z-10 bg-gray-900/50 lg:hidden dark:bg-gray-900/90"
    @click="emit('close')"
  />
</template>
