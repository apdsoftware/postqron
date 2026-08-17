import type { HexIconName } from '~/utils/icons'

/**
 * Dati dell'azienda usati dal footer e dai metadati.
 *
 * Sono segnaposto in attesa dei valori definitivi: l'indirizzo legale e i
 * profili social non sono ancora stati forniti (SPEC §7, Q3). Stanno qui e non
 * sparsi nel markup proprio perché sostituirli sia una modifica sola.
 */
export const company = {
  name: 'PostQron',
  legalName: 'PostQron',
  about:
    'Cronjob gestiti, definiti nel tuo repository e ricondotti a un solo posto: '
    + 'una schedulazione affidabile, i log di ogni esecuzione e un avviso quando '
    + 'qualcosa non parte.',
  address: 'Indirizzo da definire',
  email: 'supporto@postqron.com',
} as const

export const socialLinks: readonly { label: string, href: string, icon: HexIconName }[] = [
  { label: 'GitHub', href: 'https://github.com/apdsoftware', icon: 'githubSquare' },
  { label: 'X', href: 'https://x.com/postqron', icon: 'twitterSquare' },
  { label: 'LinkedIn', href: 'https://www.linkedin.com/company/postqron', icon: 'linkedinSquare' },
]
