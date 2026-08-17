import type { Ref } from 'vue'
import { ApiError } from '~/utils/api'

/**
 * Un dato che arriva dal backend, con i suoi tre stati dichiarati (R56).
 *
 * ## Perché non `useAsyncData` / `useFetch`
 *
 * Sono composable pensati per l'SSR: servono a trasportare nel client il dato
 * che il server ha già risolto durante il rendering. Qui l'SSR non c'è
 * (`ssr: false`, SPEC §2) e quel trasporto è peso morto — nel bundle e nel
 * modello mentale, perché `pending` in un'app senza SSR significa una cosa sola
 * e non tre. Ciò che serve davvero è più piccolo: una richiesta, tre ref, e un
 * modo di rifarla.
 *
 * ## Perché esiste invece di lasciar fare a ogni pagina
 *
 * Perché R56 chiede che *ogni* vista dichiari cosa mostra senza dati, in
 * caricamento e quando la richiesta fallisce — e una regola che va ricordata a
 * ogni vista è una regola che si perde alla terza. Qui i tre stati sono la forma
 * del valore di ritorno: una pagina che li ignora deve farlo apposta. Il
 * corrispettivo visivo è `<AsyncState>`, che riceve questi stessi tre campi.
 *
 * La richiesta parte al montaggio, non alla chiamata: un composable che parte
 * mentre il componente si sta ancora costruendo non può essere annullato quando
 * il componente sparisce.
 */
export interface ApiResource<T> {
  /** Il dato, se è arrivato. */
  data: Ref<T | null>
  /** Una richiesta è in volo. Vale anche per la prima. */
  pending: Ref<boolean>
  /** L'ultimo guasto, azzerato quando una richiesta riesce. */
  error: Ref<ApiError | null>
  /** Rifà la richiesta. È ciò che chiama il pulsante «riprova». */
  refresh: () => Promise<void>
}

export function useApiResource<T>(
  fetcher: (signal: AbortSignal) => Promise<T>,
): ApiResource<T> {
  const data = shallowRef<T | null>(null)
  const pending = ref(false)
  const error = ref<ApiError | null>(null)

  /*
   * Ogni richiesta ha il proprio controller e il proprio numero d'ordine.
   *
   * Il controller serve a chiudere la richiesta quando il componente sparisce —
   * chi cambia pagina mentre i dati arrivano non deve pagare quel trasferimento
   * né vedersi scrivere sopra uno stato che non guarda più.
   *
   * Il numero d'ordine serve a un caso più insidioso: due «riprova» ravvicinati
   * e la prima risposta che arriva dopo la seconda. Senza, l'ultima risposta
   * scritta sarebbe la più vecchia, e la schermata mostrerebbe un dato superato
   * senza che niente segnali l'inversione.
   */
  let controller: AbortController | null = null
  let latest = 0

  async function refresh(): Promise<void> {
    controller?.abort()
    controller = new AbortController()

    const ticket = ++latest
    pending.value = true

    try {
      const result = await fetcher(controller.signal)
      if (ticket !== latest) return

      data.value = result
      error.value = null
    }
    catch (cause) {
      if (ticket !== latest) return
      // Richiesta annullata: non è un esito, e chi l'ha annullata sa perché.
      if (cause instanceof DOMException && cause.name === 'AbortError') return

      /*
       * Un errore che non è un `ApiError` viene da un difetto nostro, non dalla
       * rete: rilanciarlo lo fa arrivare in console invece di travestirlo da
       * «il backend non risponde», che manderebbe a cercare nel posto sbagliato.
       */
      if (!(cause instanceof ApiError)) throw cause

      data.value = null
      error.value = cause
    }
    finally {
      if (ticket === latest) pending.value = false
    }
  }

  onMounted(refresh)
  onBeforeUnmount(() => controller?.abort())

  return { data, pending, error, refresh }
}
