import type { SiteContent } from '~/types/content'
import type { LocaleCode } from '~/utils/locale'
import { en } from '~/content/en'
import { it } from '~/content/it'
import { es } from '~/content/es'
import { de } from '~/content/de'
import { fr } from '~/content/fr'

/**
 * I contenuti delle cinque lingue, indicizzati per codice.
 *
 * `Record<LocaleCode, SiteContent>` è la garanzia strutturale del multilingua:
 * aggiungere una lingua a `LOCALE_CODES` senza il file corrispondente non
 * compila, e un file a cui manca una sezione nemmeno.
 */
export const siteContent: Record<LocaleCode, SiteContent> = { en, it, es, de, fr }
