import type { DashboardContent } from '~/types/content'
import type { LocaleCode } from '~/utils/locale'
import { en } from '~/content/en'
import { it } from '~/content/it'
import { es } from '~/content/es'
import { de } from '~/content/de'
import { fr } from '~/content/fr'

/**
 * I testi delle cinque lingue, indicizzati per codice.
 *
 * `Record<LocaleCode, DashboardContent>` è la garanzia strutturale del
 * multilingua: aggiungere una lingua a `LOCALE_CODES` senza il file
 * corrispondente non compila, e un file a cui manca una chiave nemmeno.
 */
export const dashboardContent: Record<LocaleCode, DashboardContent> = { en, it, es, de, fr }
