/**
 * Rende disponibili al codice le versioni dichiarate nei documenti legali,
 * lette da `legal/en/*.md` al momento della build (R46, cookie policy §4).
 *
 * Perché non una costante scritta a mano. Il consenso è legato alla versione
 * del documento su cui è stato prestato: se la costante e il front matter
 * divergono, o si chiede di nuovo una scelta già data, o — molto peggio — si
 * considera valido un consenso prestato su un testo che non diceva le stesse
 * cose. Le quattro versioni sono già oggi diverse fra loro (`terms-of-service`
 * 1.2.0, `privacy-policy` 1.1.0, le altre 1.0.0) e cambiano una alla volta:
 * ricopiarle a mano è una divergenza che aspetta solo il momento giusto.
 *
 * Perché non un `?raw` del Markdown. `utils/legal.ts` incorpora i quattro
 * documenti e `marked` — circa 66 KB — e per questo può essere importato solo
 * dal chunk pigro della pagina legale. Il consenso ai cookie sta invece nel
 * layout, cioè nel bundle d'ingresso di *ogni* pagina: leggere di lì la
 * versione ci trascinerebbe dentro il testo integrale della policy. Qui
 * finiscono nel bundle quattro stringhe di sei caratteri.
 *
 * Il modulo sta in `modules/`, che Nuxt registra da solo: `nuxt.config.ts`
 * resta intatto e non litiga al merge con le altre issue che lo modificano.
 */

import { readFileSync } from 'node:fs'
import { addTemplate, addTypeTemplate, defineNuxtModule } from 'nuxt/kit'
import { LEGAL_DOCUMENT_IDS } from '../utils/legal-documents'

/** Estrae `version:` dal front matter, senza toccare il testo giuridico. */
function versionOf(source: string, id: string): string {
  const version = source.match(/^---\n[\s\S]*?^version:[ \t]*(\S+)[ \t]*$/m)?.[1]
  if (!version) throw new Error(`Versione mancante nel front matter di legal/en/${id}.md`)

  return version
}

export function legalVersions(legalDir: string): Record<string, string> {
  return Object.fromEntries(
    LEGAL_DOCUMENT_IDS.map(id => [
      id,
      versionOf(readFileSync(`${legalDir}/${id}.md`, 'utf8'), id),
    ]),
  )
}

export default defineNuxtModule({
  meta: { name: 'legal-versions' },

  setup(_options, nuxt) {
    const legalDir = `${nuxt.options.rootDir}/../../legal/en`
    const versions = legalVersions(legalDir)

    const template = addTemplate({
      filename: 'legal-versions.mjs',
      write: true,
      getContents: () => `export const LEGAL_VERSIONS = ${JSON.stringify(versions, null, 2)}\n`,
    })

    addTypeTemplate({
      filename: 'legal-versions.d.ts',
      getContents: () => [
        `import type { LegalDocumentId } from '../utils/legal-documents'`,
        ``,
        `declare module '#legal-versions' {`,
        `  export const LEGAL_VERSIONS: Record<LegalDocumentId, string>`,
        `}`,
        ``,
      ].join('\n'),
    })

    nuxt.options.alias['#legal-versions'] = template.dst

    // I documenti non passano da Vite: un cambio di versione non farebbe
    // ripartire il dev server senza questo.
    nuxt.options.watch.push(...LEGAL_DOCUMENT_IDS.map(id => `${legalDir}/${id}.md`))
  },
})
