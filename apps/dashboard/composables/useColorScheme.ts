import type { Ref } from 'vue'
import type { ColorScheme } from '~/utils/color-scheme'
import { COLOR_SCHEME_STORAGE_KEY, DARK_CLASS, resolveColorScheme } from '~/utils/color-scheme'

/**
 * Tema chiaro/scuro corrente e interruttore per cambiarlo.
 *
 * Stessa impostazione di `useLocale()`: stato dell'applicazione in `useState`,
 * scelta esplicita persistita, rilevamento come ripiego. Le due preferenze si
 * somigliano perché sono la stessa cosa — come l'utente vuole vedere
 * l'interfaccia — e avere due meccanismi diversi per la stessa cosa è il modo
 * più affidabile di farne divergere uno.
 */
const STATE_KEY = 'dashboard-color-scheme'

export interface DashboardColorScheme {
  scheme: Ref<ColorScheme>
  /** `true` quando il tema corrente è quello scuro. Per il markup. */
  isDark: Ref<boolean>
  /** Registra la scelta esplicita e la applica. */
  setScheme: (value: ColorScheme) => void
  /** Passa all'altro tema. È ciò che fa l'interruttore nella barra superiore. */
  toggle: () => void
}

function storedScheme(): string | null {
  try {
    return window.localStorage.getItem(COLOR_SCHEME_STORAGE_KEY)
  }
  catch {
    // Storage non disponibile: si perde solo la memoria della scelta.
    return null
  }
}

function rememberScheme(scheme: ColorScheme): void {
  try {
    window.localStorage.setItem(COLOR_SCHEME_STORAGE_KEY, scheme)
  }
  catch {
    // Nessun rimedio possibile: il tema resta valido per questa visita.
  }
}

function prefersDark(): boolean | undefined {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return undefined

  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

function initialScheme(): ColorScheme {
  if (typeof window === 'undefined') return 'light'

  return resolveColorScheme({ stored: storedScheme(), prefersDark: prefersDark() })
}

export function useColorScheme(): DashboardColorScheme {
  const scheme = useState<ColorScheme>(STATE_KEY, initialScheme)
  const isDark = computed(() => scheme.value === 'dark')

  /*
   * La classe la scrive `useHead`, non `document.documentElement.classList`.
   *
   * È la stessa ragione per cui `lang` sta in `app.vue`: due sorgenti che
   * scrivono lo stesso attributo si sovrascrivono a vicenda in ordine
   * imprevedibile, e con `useHead` la classe è una funzione dello stato invece
   * che il risultato di chi ha scritto per ultimo. Lo script in `nuxt.config.ts`
   * la mette prima che Vue esista — poi non la tocca più.
   */
  useHead(computed(() => ({
    htmlAttrs: { class: scheme.value === 'dark' ? DARK_CLASS : '' },
  })))

  function setScheme(value: ColorScheme) {
    scheme.value = value
    rememberScheme(value)
  }

  return {
    scheme,
    isDark,
    setScheme,
    toggle: () => setScheme(scheme.value === 'dark' ? 'light' : 'dark'),
  }
}
