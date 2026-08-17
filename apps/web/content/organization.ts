/**
 * Dati identificativi dell'impresa, in forma strutturata.
 *
 * Non stanno in `content/<lingua>.ts` per la stessa ragione dei profili social:
 * non c'è nulla da tradurre. Un indirizzo postale si scrive come lo scrive la
 * posta del paese in cui si trova, e una partita IVA è un numero.
 *
 * Le stesse informazioni compaiono già in `company.address`, che però è **una
 * riga formattata per essere letta**: i dati strutturati vogliono i campi
 * separati, e spezzare quella riga con un'espressione regolare significherebbe
 * far dipendere il JSON-LD dalla punteggiatura di cinque traduzioni.
 * `test/structured-data.test.ts` verifica che le due forme non divergano.
 */

export const ORGANIZATION_ADDRESS = {
  streetAddress: 'Via C. Colombo 15',
  postalCode: '24047',
  addressLocality: 'Treviglio',
  addressRegion: 'BG',
  /** ISO 3166-1 alpha-2, come vuole schema.org. */
  addressCountry: 'IT',
} as const

/** Partita IVA, nella forma in cui è pubblicata sul sito. */
export const ORGANIZATION_VAT_ID = '03835250162'
