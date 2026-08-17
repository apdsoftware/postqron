import type { SocialLink } from '~/types/content'

/**
 * Profili social, uguali in tutte le lingue.
 *
 * Non stanno in `content/<lingua>.ts` perché non c'è nulla da tradurre: le
 * etichette sono nomi propri di servizi e gli indirizzi sono gli stessi ovunque.
 * Duplicarli cinque volte significherebbe solo poterli sbagliare cinque volte.
 *
 * Sono segnaposto in attesa dei valori definitivi (SPEC §7, Q3).
 */
export const socialLinks: readonly SocialLink[] = [
  { label: 'GitHub', href: 'https://github.com/apdsoftware', icon: 'githubSquare' },
  { label: 'X', href: 'https://x.com/postqron', icon: 'twitterSquare' },
  { label: 'LinkedIn', href: 'https://www.linkedin.com/company/postqron', icon: 'linkedinSquare' },
]
