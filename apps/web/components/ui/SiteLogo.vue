<script setup lang="ts">
/**
 * Marchio PostQron.
 *
 * È SVG inline e non un `<img>` perché il logotipo è composto in Quicksand: un
 * SVG referenziato da `src` viene renderizzato in un documento isolato, senza
 * accesso ai `@font-face` della pagina, e il testo tornerebbe al sans di
 * sistema. Inline, invece, eredita font e colore dal documento.
 */
withDefaults(
  defineProps<{
    /** Altezza resa, in pixel: il tema usa 37 nell'header e nel footer. */
    height?: number
  }>(),
  { height: 37 },
)
</script>

<template>
  <svg
    class="site-logo"
    viewBox="0 0 138 37"
    :height="height"
    :width="(height * 138) / 37"
    role="img"
  >
    <title>PostQron</title>
    <defs>
      <linearGradient
        id="pq-logo-mark"
        x1="0"
        y1="1"
        x2="1"
        y2="0"
      >
        <stop
          offset="0"
          stop-color="#0fb4e5"
        />
        <stop
          offset="1"
          stop-color="#743fe5"
        />
      </linearGradient>
    </defs>
    <!-- Barre di altezza irregolare: le tacche di una schedulazione. -->
    <g fill="url(#pq-logo-mark)">
      <rect
        x="0"
        y="11"
        width="4"
        height="15"
        rx="2"
      />
      <rect
        x="7"
        y="4"
        width="4"
        height="29"
        rx="2"
      />
      <rect
        x="14"
        y="8"
        width="4"
        height="21"
        rx="2"
      />
      <rect
        x="21"
        y="0"
        width="4"
        height="37"
        rx="2"
      />
      <rect
        x="28"
        y="13"
        width="4"
        height="11"
        rx="2"
      />
    </g>
    <text
      x="41"
      y="19"
      class="site-logo__word"
      font-size="17"
      letter-spacing="0.4"
      dominant-baseline="central"
    >
      PostQron
    </text>
  </svg>
</template>

<style scoped>
.site-logo {
  display: block;
}

.site-logo__word {
  font-family: var(--pq-font);
  font-weight: 500;
  fill: var(--pq-logo-color, var(--pq-heading));
  transition: var(--pq-transition);
}
</style>
