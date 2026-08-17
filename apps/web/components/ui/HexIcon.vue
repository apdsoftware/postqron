<script setup lang="ts">
import { hexIcons, type HexIconName } from '~/utils/icons'

const props = defineProps<{
  /** Nome del glifo, fra quelli dichiarati in `utils/icons.ts`. */
  name: HexIconName
  /**
   * Testo alternativo. Se assente l'icona è decorativa e viene nascosta agli
   * screen reader: è il caso più comune, perché nel tema l'icona affianca
   * quasi sempre un'etichetta già leggibile.
   */
  label?: string
}>()

const glyph = computed(() => hexIcons[props.name])
</script>

<template>
  <svg
    class="hex-icon"
    :viewBox="`0 0 ${glyph.width} 1792`"
    :style="{ width: `${glyph.width / 1792}em` }"
    :role="label ? 'img' : undefined"
    :aria-hidden="label ? undefined : 'true'"
    focusable="false"
  >
    <title v-if="label">{{ label }}</title>
    <!--
      Il font ha l'asse Y rivolto verso l'alto e la baseline a 1536: la
      trasformazione riporta il tracciato nel sistema di coordinate SVG.
    -->
    <path
      :d="glyph.path"
      transform="translate(0, 1536) scale(1, -1)"
    />
  </svg>
</template>

<style scoped>
.hex-icon {
  display: inline-block;
  height: 1em;

  /*
   * La discesa del font è 256/1792 em sotto la baseline: allineando lì il
   * bordo inferiore, l'icona sta sulla riga di testo esattamente come faceva
   * il glifo del webfont.
   */
  vertical-align: -0.142857em;
  fill: currentcolor;
}
</style>
