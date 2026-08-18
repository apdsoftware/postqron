/**
 * Lingue della dashboard, e il modo in cui la lingua sta nell'indirizzo
 * (SPEC §8-bis, R31–R33).
 *
 * ## Perché il prefisso vale anche qui
 *
 * §8-bis prescrive le rotte prefissate — `/en/`, `/it/`, `/es/`, `/de/`,
 * `/fr/` — su **entrambe** le applicazioni. Che la dashboard non vada
 * indicizzata toglie la ragione *SEO* del prefisso, non le altre, e le altre
 * bastano da sole:
 *
 * 1. **Un indirizzo che non porta con sé la lingua non è condivisibile né
 *    collegabile.** Chi manda a un collega il link di una schermata gli manda
 *    la schermata; senza prefisso gli manda anche il proprio browser, perché la
 *    lingua che si apre dall'altra parte è quella indovinata là.
 * 2. **Chi compone un link da fuori deve poter dire in che lingua aprirlo.** Le
 *    email transazionali (R21) lo fanno già: il backend le compone con
 *    `AppURL()` (`services/api/internal/emailrender/context.go`), che antepone
 *    la lingua del profilo del destinatario (R33) e produce `/it/jobs/new`.
 *    Finché la dashboard non ha avuto quelle rotte, **ogni link di ogni email
 *    è caduto sulla pagina «non trovata»** — e con la guardia di sessione ci si
 *    arrivava dopo aver scritto la password.
 *
 * ## Prefissata non vuol dire indicizzabile
 *
 * È la distinzione che mancava, e va tenuta ferma: qui **non** esistono
 * `hreflang`, `canonical` né sitemap, il guscio dichiara `noindex` su ogni
 * indirizzo (`app.vue`) e `public/robots.txt` **non** vieta la scansione,
 * perché un `Disallow` impedirebbe al crawler di leggere proprio quel
 * `noindex`. Il prefisso serve a chi condivide un link, non a Google.
 *
 * ## Indirizzo e profilo: chi vince
 *
 * Sono due sorgenti della stessa cosa, e la precedenza è **dichiarata**:
 *
 * - **L'indirizzo comanda la pagina che si sta guardando.** `/de/jobs/42` si
 *   apre in tedesco anche se il profilo dice italiano. Il contrario
 *   renderebbe il prefisso decorativo: il link condiviso si aprirebbe nella
 *   lingua di chi lo riceve, cioè esattamente il difetto che il prefisso
 *   esiste per togliere.
 * - **Il profilo decide dove si atterra quando l'indirizzo non dice niente** —
 *   la radice `/`, un vecchio segnalibro, un indirizzo scritto a mano. Lì è la
 *   risposta migliore disponibile, e batte sia la scelta memorizzata in questo
 *   browser sia il rilevamento, perché è una preferenza della *persona* e non
 *   del portatile da cui si è collegata oggi. È l'ordine di [resolveLocale].
 *
 * Normalmente le due sorgenti coincidono, ed è il caso delle email: il link è
 * composto **dal profilo**. Il caso in cui divergono è quello interessante — un
 * indirizzo condiviso da qualcun altro — ed è quello in cui vince l'indirizzo.
 *
 * Il rovescio della medaglia è dichiarato anch'esso: guardare una schermata in
 * tedesco perché qualcuno ne ha condiviso il link **non cambia la lingua del
 * profilo**. Solo il selettore scrive una preferenza; l'indirizzo la usa e
 * basta. Altrimenti aprire il link di un collega riscriverebbe in silenzio la
 * lingua delle proprie email.
 *
 * Questo modulo è deliberatamente privo di dipendenze da Vue e da Nuxt: è
 * logica pura, quindi verificabile senza montare nulla.
 */

/** Codici ISO 639-1 delle lingue supportate. L'ordine è quello del selettore. */
export const LOCALE_CODES = ['en', 'it', 'es', 'de', 'fr'] as const

export type LocaleCode = (typeof LOCALE_CODES)[number]

/**
 * Lingua predefinita e **sorgente** dei contenuti (SPEC §8-bis): si scrive in
 * inglese e si traduce, non il contrario. È anche il ripiego quando il browser
 * non chiede nessuna delle cinque.
 */
export const DEFAULT_LOCALE: LocaleCode = 'en'

export interface LocaleDescriptor {
  code: LocaleCode
  /**
   * Nome della lingua **nella lingua stessa**. Non è contenuto traducibile: in
   * un selettore «Deutsch» si chiama così anche per chi legge in italiano, ed è
   * l'unica forma che chi non capisce la lingua corrente sa riconoscere.
   */
  label: string
  /** Valore dell'attributo `lang` del documento. */
  htmlLang: string
}

export const LOCALES: readonly LocaleDescriptor[] = [
  { code: 'en', label: 'English', htmlLang: 'en' },
  { code: 'it', label: 'Italiano', htmlLang: 'it' },
  { code: 'es', label: 'Español', htmlLang: 'es' },
  { code: 'de', label: 'Deutsch', htmlLang: 'de' },
  { code: 'fr', label: 'Français', htmlLang: 'fr' },
]

/**
 * Chiave della scelta esplicita dell'utente in `localStorage`.
 *
 * R32: la scelta fatta col selettore prevale sul rilevamento e persiste fra le
 * visite. Il valore è il solo codice della lingua.
 *
 * Non è in concorrenza con il prefisso: il prefisso dice in che lingua è la
 * pagina aperta adesso, questa chiave dice in quale aprire un indirizzo che non
 * lo dichiara. Vedi la precedenza in testa al modulo.
 *
 * È la stessa chiave del sito pubblico di proposito: se un giorno le due app
 * finissero sulla stessa origin, chi ha scelto lo spagnolo su postqron.com si
 * ritroverebbe la dashboard in spagnolo, che è il comportamento voluto. Finché
 * stanno su sottodomini diversi i due valori restano semplicemente separati.
 */
export const LOCALE_STORAGE_KEY = 'postqron:locale'

export function isLocaleCode(value: unknown): value is LocaleCode {
  return typeof value === 'string' && (LOCALE_CODES as readonly string[]).includes(value)
}

/**
 * Sceglie la lingua a partire dalle preferenze del browser (R31).
 *
 * Confronta il solo sottotag primario: `it-CH` e `it` vogliono entrambi
 * l'italiano, e non abbiamo varianti regionali da distinguere. Se nessuna delle
 * preferenze corrisponde si usa l'inglese.
 *
 * @param preferred lista in ordine di preferenza, tipicamente `navigator.languages`
 */
export function detectLocale(preferred: readonly string[] | undefined): LocaleCode {
  for (const tag of preferred ?? []) {
    const primary = tag.toLowerCase().split('-')[0]
    if (isLocaleCode(primary)) return primary
  }

  return DEFAULT_LOCALE
}

/**
 * Le tre sorgenti da cui può arrivare la lingua **quando l'indirizzo non la
 * dice**, in ordine di autorità.
 *
 * L'indirizzo non compare qui, e non è una dimenticanza: quando c'è, non c'è
 * niente da risolvere — è lui la lingua della pagina, e questa funzione non
 * viene nemmeno chiamata (vedi `middleware/01.locale.global.ts`).
 *
 * Ogni campo è `unknown` per costruzione: sono valori che entrano dall'esterno
 * — una risposta dell'API, una chiave di `localStorage` scritta da una versione
 * precedente, la lista del browser — e la validazione è compito di questo
 * modulo, non di chi lo chiama.
 */
export interface LocalePreferences {
  /**
   * **Punto di innesto di R33** (colonna `users.language`, migrazione 0015).
   *
   * La lingua che l'utente autenticato ha impostato nel proprio profilo vale su
   * tutte le sue sessioni: è una preferenza della persona, non del browser da
   * cui si è collegata oggi. Per questo sta in cima all'ordine e batte sia il
   * rilevamento sia la scelta locale — altrimenti un nuovo portatile
   * smisterebbe su una lingua diversa dal telefono, pur essendo lo stesso
   * utente. È anche la lingua con cui il backend compone i link delle email
   * (R21), quindi far coincidere lo smistamento con essa è ciò che rende
   * indistinguibili i due percorsi d'ingresso.
   *
   * Oggi vale sempre `null`: `UserResponse` non espone ancora `language`
   * (`services/api/internal/httpapi/auth.go`), e aggiungercelo è lavoro della
   * issue del profilo utente, insieme al selettore che lo scrive. Quando ci
   * sarà, l'unico punto da cambiare è `useLocale()`, che passa qui il campo
   * della sessione già caricata.
   */
  profile?: unknown
  /** Scelta esplicita fatta col selettore in una visita precedente (R32). */
  stored?: unknown
  /** Preferenze del browser, tipicamente `navigator.languages` (R31). */
  browser?: readonly string[]
}

/**
 * Applica l'ordine di precedenza fra le sorgenti: profilo, scelta memorizzata,
 * browser, e infine l'inglese.
 *
 * È l'unico posto in cui quell'ordine è scritto. Sta qui, in logica pura,
 * perché è la regola che i requisiti descrivono davvero (R31–R33) ed è la sola
 * cosa che vale la pena verificare senza un browser: tutto il resto —
 * `localStorage`, `navigator`, lo stato di Vue — è trasporto.
 */
export function resolveLocale({ profile, stored, browser }: LocalePreferences): LocaleCode {
  if (isLocaleCode(profile)) return profile
  if (isLocaleCode(stored)) return stored

  return detectLocale(browser)
}

/*
 * ------------------------------------------------------------------ percorsi
 *
 * Le tre funzioni che seguono sono il gemello di quelle di `apps/web`, con una
 * differenza sola e voluta: **niente slash finale**.
 *
 * Là ogni rotta è una directory pre-renderizzata con dentro `index.html`, e su
 * Cloudflare Pages la forma senza slash è un redirect verso quella con lo
 * slash: un `canonical` che punta a un redirect è un canonical sbagliato. Qui
 * non c'è né pre-rendering né canonical — c'è un guscio unico servito su
 * qualunque percorso (`public/_redirects`) e un router lato client — quindi lo
 * slash finale non comprerebbe niente e renderebbe `/it/` diverso da `/it` in
 * ogni confronto fra stringhe. La forma senza è anche quella che compone
 * `AppURL()`: `base + "/" + lingua + path`.
 *
 * Query e ancora non passano di qui, ed è l'altra differenza: sul sito i
 * percorsi arrivano da `content/` come stringhe con dentro `#pricing`, mentre
 * nella dashboard viaggiano nell'oggetto rotta di vue-router, dove `query` e
 * `hash` sono campi a sé. Farli attraversare una funzione di stringhe
 * significherebbe rimontarli e riscomporli per niente.
 */

/** Spezza un percorso nei suoi segmenti non vuoti. */
function segmentsOf(path: string): string[] {
  return path.split('/').filter(Boolean)
}

/**
 * Antepone la lingua a un percorso della dashboard.
 *
 * I percorsi scritti nel codice — il registro della navigazione, `LOGIN_PATH`,
 * il link «torna alla panoramica» — sono **senza lingua**, perché la stessa
 * voce vale per tutte e cinque. È qui che diventano `/it`, `/it/jobs/42`.
 *
 * Un percorso che ha già un prefisso non viene prefissato due volte: chiamarla
 * su un valore che arriva dalla rotta corrente è un errore facile, e
 * `/it/it/jobs` non è un indirizzo che qualcuno debba scoprire aprendolo.
 */
export function localePath(path: string, locale: LocaleCode): string {
  return `/${[locale, ...segmentsOf(stripLocale(path))].join('/')}`
}

/**
 * Toglie il prefisso di lingua, restituendo la forma neutra accettata da
 * [localePath].
 *
 * Serve al selettore, che deve comporre l'indirizzo della pagina corrente nelle
 * altre quattro lingue, e a chiunque confronti la rotta corrente con un
 * percorso scritto nel codice — l'evidenziazione della voce di menu, per
 * esempio, che non deve dipendere dalla lingua.
 *
 * Un percorso che non comincia con una delle cinque lingue torna com'è: è il
 * caso di un vecchio segnalibro su `/jobs/42`, che il middleware prefisserà.
 */
export function stripLocale(path: string): string {
  const segments = segmentsOf(path)
  if (isLocaleCode(segments[0])) segments.shift()

  return `/${segments.join('/')}`
}

/**
 * Estrae la lingua dal primo segmento del percorso, o `null` se non ce n'è una.
 *
 * `null` è la domanda a cui risponde il middleware di smistamento, ed è il solo
 * modo per distinguere `/de/jobs` — tedesco — da `/jobs`, che di lingue non ne
 * dichiara nessuna. È anche il motivo per cui i cinque codici sono un elenco
 * chiuso: `/jobs` non deve diventare la lingua «jobs».
 */
export function localeFromPath(path: string): LocaleCode | null {
  const first = segmentsOf(path)[0]

  return isLocaleCode(first) ? first : null
}
