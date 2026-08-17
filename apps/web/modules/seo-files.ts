/**
 * Scrive `robots.txt` e `sitemap.xml` nell'output statico (R53-ter).
 *
 * Perché un modulo e non una rotta. Su Cloudflare Pages viene pubblicato solo
 * `.output/public` e non resta alcun Nitro (SPEC §2): una rotta che generasse
 * questi file a runtime non risponderebbe a nessuno, e `nuxt.config.ts` vieta
 * esplicitamente di introdurne. Devono quindi esistere come file.
 *
 * Il modulo sta in `modules/`, che Nuxt registra da solo: `nuxt.config.ts`
 * resta intatto, e questa funzionalità non litiga al merge con le altre che lo
 * stanno modificando.
 *
 * Il momento è `prerender:done`: prima non si saprebbe quali pagine sono state
 * davvero generate, e sono proprio quelle che la sitemap deve dichiarare.
 */

import { writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { defineNuxtModule } from 'nuxt/kit'
import { renderRobots, renderSitemap, sitemapEntries } from '~/utils/sitemap'

/**
 * Ogni rotta pre-renderizzata è una directory con dentro `index.html`, e sia i
 * percorsi della sitemap sia quelli riportati da Nitro chiudono con lo slash.
 * La normalizzazione è comunque necessaria: `crawlLinks` scopre gli indirizzi
 * leggendoli dall'HTML, e da lì possono arrivare in una forma qualsiasi.
 */
function normalize(route: string): string {
  const path = route.split('#')[0]!.split('?')[0]!
  const segments = path.split('/').filter(Boolean)

  return segments.length === 0 ? '/' : `/${segments.join('/')}/`
}

export default defineNuxtModule({
  meta: { name: 'seo-files' },

  setup(_options, nuxt) {
    nuxt.hook('nitro:init', (nitro) => {
      nitro.hooks.hook('prerender:done', async ({ prerenderedRoutes }) => {
        /*
         * L'origin del sito è nota solo al momento della build: su hosting
         * statico non c'è un processo che possa leggerla dopo (`NUXT_PUBLIC_*`
         * finisce nel bundle). Nitro applica l'ambiente al proprio
         * runtimeConfig solo all'avvio, che qui non avviene mai: la variabile
         * va letta direttamente.
         */
        const siteUrl = process.env.NUXT_PUBLIC_SITE_URL
          ?? String(nitro.options.runtimeConfig.public.siteUrl)

        /*
         * Le pagine davvero generate. Una pagina è una directory con dentro
         * `index.html`: restano fuori i payload `.json` e i due file di
         * fallback del routing, `200.html` e `404.html`, che non sono
         * indirizzi da indicizzare.
         */
        const generated = new Set(
          prerenderedRoutes
            .filter(route => !route.error && !route.skip)
            .filter(route => route.fileName?.endsWith('index.html'))
            .map(route => normalize(route.route)),
        )

        const entries = sitemapEntries(siteUrl)
        const declared = new Set(entries.map(entry => normalize(entry.path)))

        /*
         * I due controlli che rendono la sitemap una descrizione del sito
         * invece di una dichiarazione d'intenti, e che costano una build rossa
         * invece di un danno silenzioso in produzione.
         *
         * Il primo: una sitemap che elenca un indirizzo mai generato manda i
         * motori su un 404, ed è un danno — meglio nessuna sitemap.
         *
         * Il secondo, meno ovvio e più utile nel tempo: una pagina generata ma
         * non elencata è una pagina che nessuno troverà. È il modo in cui una
         * sitemap invecchia — le pagine sono 45 e cresceranno — e senza questo
         * controllo l'unico segnale arriverebbe mesi dopo, dalle statistiche.
         */
        const missing = [...declared].filter(path => !generated.has(path))
        if (missing.length > 0) {
          throw new Error(
            `sitemap.xml dichiara pagine che non sono state generate: ${missing.join(', ')}`,
          )
        }

        const unlisted = [...generated].filter(path => path !== '/' && !declared.has(path))
        if (unlisted.length > 0) {
          throw new Error(
            'pagine generate ma assenti dalla sitemap: '
            + `${unlisted.join(', ')} — aggiungile a SITE_PATHS in utils/sitemap.ts`,
          )
        }

        const publicDir = nitro.options.output.publicDir
        await writeFile(join(publicDir, 'robots.txt'), renderRobots(siteUrl), 'utf8')
        await writeFile(join(publicDir, 'sitemap.xml'), renderSitemap(siteUrl), 'utf8')

        nitro.logger.success(`robots.txt e sitemap.xml (${entries.length} indirizzi)`)
      })
    })
  },
})
