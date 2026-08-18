/**
 * Riempie i segnaposti di una frase tradotta.
 *
 * ## Perché serve, visto che i testi sono costanti
 *
 * Quasi tutte le frasi della dashboard sono fisse, e va benissimo. Alcune però
 * devono nominare un valore che arriva dal backend — il tetto del piano, la
 * risoluzione minima, il fuso del job — e per quelle ci sono due strade:
 * spezzare la frase in due metà e mettere il valore in mezzo, oppure lasciarla
 * intera con un `{nome}` dentro.
 *
 * La prima non si traduce. «{n} di {max} cronjob» in tedesco non ha le parti
 * nello stesso ordine, e una traduttrice che riceve due mezze frasi non sa
 * nemmeno che vanno insieme. La seconda dà una frase completa, con il posto del
 * valore dichiarato: è la sola forma su cui si possa lavorare.
 *
 * ## Cosa non fa
 *
 * Non è un motore di traduzione e non deve diventarlo: niente plurali, niente
 * scelte condizionali, niente formattazione di numeri. I plurali si risolvono
 * scrivendo frasi che non ne hanno bisogno — «Cronjob: 3 di 20» invece di «hai 3
 * cronjob» — che è anche il modo in cui restano leggibili in cinque lingue con
 * regole di plurale diverse. Le date le formatta `Intl`, che le lingue le
 * conosce già.
 *
 * Un segnaposto senza valore resta scritto com'è. Non è tolleranza: una frase a
 * cui manca `{plan}` è un difetto, e vederlo sullo schermo è il modo più veloce
 * di accorgersene — mentre una stringa vuota al suo posto produrrebbe una frase
 * plausibile e monca.
 */
export function fill(template: string, values: Record<string, string | number>): string {
  return template.replace(/\{(\w+)\}/g, (match, key: string) => {
    const value = values[key]
    return value === undefined ? match : String(value)
  })
}
