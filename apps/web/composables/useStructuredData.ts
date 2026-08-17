import type { SchemaNode } from '~/utils/structured-data'
import { structuredData } from '~/utils/structured-data'

/**
 * Aggiunge il grafo JSON-LD della pagina (R53-ter).
 *
 * Uno `<script>` per pagina, non uno per nodo: i nodi si riferiscono per `@id`
 * e in un grafo solo quei riferimenti si risolvono da soli.
 *
 * Il contenuto è già serializzato e passa da `innerHTML` senza altre
 * trasformazioni: `structuredData` neutralizza il `<`, che è l'unico carattere
 * capace di chiudere l'elemento in anticipo.
 */
export function useStructuredData(nodes: readonly SchemaNode[]): void {
  useHead({
    script: [{ type: 'application/ld+json', innerHTML: structuredData(nodes) }],
  })
}
