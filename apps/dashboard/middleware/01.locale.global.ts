import { localeFromPath, localePath } from '~/utils/locale'

/**
 * Il prefisso di lingua nell'indirizzo (SPEC §8-bis, R31–R33).
 *
 * ## Cosa decide
 *
 * Una cosa sola: **se l'indirizzo dichiara una lingua, la si tiene; se non la
 * dichiara, gliela si mette e si riparte da lì.** Non c'è un terzo caso, e non
 * c'è nessun punto in cui la lingua della pagina venga decisa altrove — chi la
 * legge (`useLocale()`) la prende dal percorso.
 *
 * Che l'indirizzo comandi è la precedenza scelta, ed è argomentata in
 * `utils/locale.ts`: se il profilo dell'utente potesse scavalcarlo, il prefisso
 * diventerebbe decorativo e un link condiviso si aprirebbe di nuovo nella
 * lingua di chi lo riceve — cioè il difetto che il prefisso esiste per togliere.
 * Il profilo, e a scendere la scelta memorizzata e il browser, decidono invece
 * *dove* si atterra qui sotto, quando l'indirizzo non dice niente.
 *
 * ## Perché un middleware e non le sole rotte
 *
 * Perché `pages/[locale]/` accetta come lingua qualunque primo segmento:
 * `/jobs/42` combacia con `[locale]/[...slug]` e vale `locale = "jobs"`. Il
 * file system non sa esprimere «uno di questi cinque», e l'elenco chiuso deve
 * stare da qualche parte. Qui è anche il posto giusto per il resto: un vecchio
 * segnalibro senza prefisso non deve dare «non trovata», deve arrivare dove
 * arrivava prima, in una lingua.
 *
 * ## Perché prima della guardia di sessione
 *
 * Il nome comincia con `01.` e quello della guardia con `02.` perché Nuxt
 * esegue i middleware globali in ordine alfabetico, e l'ordine qui è
 * sostanziale. Un `navigateTo` restituito da un middleware interrompe la
 * navigazione: se girasse prima la guardia, `/jobs/42` da scollegati
 * produrrebbe `?next=/jobs/42` — l'indirizzo *senza* lingua — e dopo l'accesso
 * si tornerebbe a un percorso che va smistato di nuovo, cioè in una lingua che
 * potrebbe non essere quella in cui si è appena letta la schermata di accesso.
 * Girando prima questo, la guardia vede solo indirizzi già prefissati e non ha
 * bisogno di sapere che le lingue esistono.
 */
export default defineNuxtRouteMiddleware((to) => {
  // L'indirizzo dichiara la lingua: è lui la risposta, e non c'è altro da fare.
  if (localeFromPath(to.path)) return

  /*
   * `replace: true`: lo smistamento non è una tappa del percorso di nessuno.
   * Lasciarlo nella cronologia farebbe sì che «indietro» dalla panoramica
   * riporti sulla radice, che rimanderebbe subito avanti — il rimbalzo senza
   * uscita apparente da cui la dashboard si guarda già altrove.
   *
   * `query` e `hash` viaggiano con l'oggetto rotta e non nel percorso: un
   * segnalibro su `/jobs?stato=fallito` deve arrivare a `/it/jobs?stato=fallito`
   * con i suoi filtri, altrimenti smistare significherebbe perdere il posto.
   */
  return navigateTo(
    { path: localePath(to.path, useLocale().preferred.value), query: to.query, hash: to.hash },
    { replace: true },
  )
})
