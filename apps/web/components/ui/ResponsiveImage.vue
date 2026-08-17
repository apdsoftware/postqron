<script setup lang="ts">
import { MODERN_FORMATS, RASTER_IMAGES, fallbackUrl, srcSet, widestUrl, type RasterImageName } from '~/utils/images'

// Gli attributi non dichiarati vanno sull'`<img>`, non sulla radice: `class` e
// `style` del chiamante descrivono l'immagine, e `<picture>` non ha layout.
defineOptions({ inheritAttrs: false })

/**
 * Fotografia servita nei formati moderni, con ripiego e larghezze multiple.
 *
 * Tutto ciò che serve arriva dal registro (`utils/images.ts`): il chiamante dice
 * *quale* immagine e *quanto larga* la mostra, non quanti pixel ha o dove stanno
 * i file. `width` e `height` non sono opzionali e non si possono sbagliare —
 * vengono dal file vero — ed è quello che impedisce lo spostamento di contenuto
 * quando l'immagine arriva (R53-bis).
 *
 * `<picture>` è dichiarato `display: contents`: non è una scatola, sceglie la
 * sorgente e sparisce dal layout. Le regole del chiamante continuano a valere
 * sull'`<img>` come se fosse rimasto figlio diretto, purché scritte con
 * `:deep(img)` — lo stile con ambito del genitore marca la radice del
 * componente, che qui è `<picture>` e non l'immagine.
 */
const props = withDefaults(
  defineProps<{
    name: RasterImageName
    alt: string
    /**
     * Larghezza di rendering, nella sintassi dell'attributo `sizes`. Va omessa
     * solo per le immagini a larghezza unica, dove non c'è niente da scegliere.
     */
    sizes?: string
    /**
     * Vero solo per l'elemento LCP. Toglie il caricamento differito e alza la
     * priorità di rete: metterlo su un'immagine sotto la piega è peggio che non
     * metterlo affatto, perché ruba banda a quella che si vede davvero.
     */
    priority?: boolean
  }>(),
  { sizes: undefined, priority: false },
)

const image = computed(() => RASTER_IMAGES[props.name])
</script>

<template>
  <picture>
    <source
      v-for="format in MODERN_FORMATS"
      :key="format"
      :type="`image/${format}`"
      :srcset="srcSet(name, format) ?? widestUrl(name, format)"
      :sizes="sizes"
    >
    <img
      v-bind="$attrs"
      :src="fallbackUrl(name)"
      :srcset="srcSet(name, image.fallback)"
      :sizes="sizes"
      :alt="alt"
      :width="image.width"
      :height="image.height"
      :loading="priority ? undefined : 'lazy'"
      :decoding="priority ? undefined : 'async'"
      :fetchpriority="priority ? 'high' : undefined"
    >
  </picture>
</template>

<style scoped>
picture {
  display: contents;
}
</style>
