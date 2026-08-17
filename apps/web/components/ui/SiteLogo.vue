<script setup lang="ts">
import { MARCHIO, SIMBOLO_STROKE, SIMBOLO_TRACCIATI } from '~/utils/marchio'

/**
 * Marchio Postqron (R34).
 *
 * È SVG inline e non un `<img>` perché deve ereditare il colore dal contesto:
 * l'header lo mostra scuro su fondo chiaro e invertito su fondo pieno, e un SVG
 * referenziato da `src` vive in un documento isolato che non vede né i colori
 * né i token della pagina.
 *
 * Le lettere sono tracciati, non testo: un logotipo composto a runtime dipende
 * dal caricamento del font e, nel frattempo, mostra il nome nel carattere di
 * sistema. Il disegno arriva da `~/utils/marchio`, generato dal kit in
 * `design/marchio/` — le regole d'uso stanno nel README di quella cartella.
 */
const props = withDefaults(
  defineProps<{
    /** Altezza resa, in pixel. Copre dalle maiuscole al fondo della discendente. */
    height?: number
    /**
     * `primaria` — simbolo a gradiente e lettere in blu profondo, su fondo chiaro.
     * `invertita` — tutto bianco, per fondi pieni e immagini.
     * `mono` — tutto del colore ereditato, per stampa e contesti a un colore.
     */
    variant?: 'primaria' | 'invertita' | 'mono'
    /**
     * Testo alternativo. Da omettere quando il marchio sta dentro un elemento
     * già etichettato — il link «torna alla home» dell'header — perché in quel
     * caso è decorativo e ripeterlo lo fa annunciare due volte.
     */
    label?: string
  }>(),
  { height: 32, variant: 'primaria', label: undefined },
)

/*
 * Il gradiente vive in un `<defs>` e si riferisce per id: con header e footer
 * sulla stessa pagina ce ne sono due, e due id uguali nello stesso documento
 * fanno vincere il primo. `useId()` ne dà uno diverso per istanza, stabile fra
 * il render del server e quello del client.
 */
const uid = useId()

/*
 * La vernice del simbolo si calcola qui e non nel CSS: un simbolo a tratto e
 * uno pieno vogliono proprietà diverse — `stroke` contro `fill` — e regole che
 * le dichiarassero entrambe finirebbero per contornare di un'unità anche i
 * simboli pieni, che sullo spazio da 32 è un ingrassamento visibile.
 */
const vernice = computed(() => ({
  primaria: `url(#${uid})`,
  invertita: 'var(--pq-logo-ink-inverted)',
  mono: 'currentcolor',
}[props.variant]))

const riempimento = computed(() => (SIMBOLO_STROKE ? 'none' : vernice.value))
const contorno = computed(() => (SIMBOLO_STROKE ? vernice.value : undefined))
</script>

<template>
  <svg
    class="site-logo"
    :class="`site-logo--${variant}`"
    :viewBox="MARCHIO.viewBox"
    :height="height"
    :width="(height * MARCHIO.larghezza) / MARCHIO.altezza"
    :role="label ? 'img' : undefined"
    :aria-hidden="label ? undefined : true"
    focusable="false"
  >
    <title v-if="label">{{ label }}</title>
    <defs>
      <!--
        Coordinate utente e non riquadro dell'oggetto: le forme del simbolo si
        sovrappongono, e con `objectBoundingBox` ognuna prenderebbe la sfumatura
        intera sul proprio riquadro invece di una sfumatura sola attraverso il
        disegno.
      -->
      <linearGradient
        :id="uid"
        gradientUnits="userSpaceOnUse"
        x1="0"
        y1="32"
        x2="32"
        y2="0"
      >
        <stop
          offset="0"
          stop-color="var(--pq-accent-start)"
        />
        <stop
          offset="1"
          stop-color="var(--pq-accent-end)"
        />
      </linearGradient>
    </defs>

    <!-- Le lettere sono disegnate sulla linea di base: il gruppo la porta a posto. -->
    <g :transform="`translate(0 ${MARCHIO.lineaDiBase})`">
      <g :transform="MARCHIO.simboloTransform">
        <path
          v-for="d in SIMBOLO_TRACCIATI"
          :key="d"
          class="site-logo__mark"
          fill-rule="evenodd"
          :fill="riempimento"
          :stroke="contorno"
          :stroke-width="SIMBOLO_STROKE || undefined"
          :stroke-linecap="SIMBOLO_STROKE ? 'round' : undefined"
          :stroke-linejoin="SIMBOLO_STROKE ? 'round' : undefined"
          :d="d"
        />
      </g>

      <g :transform="MARCHIO.logoTransform">
        <path
          class="site-logo__word"
          :d="MARCHIO.lettere"
        />
      </g>
    </g>
  </svg>
</template>

<style scoped>
.site-logo {
  display: block;
}

.site-logo__word {
  fill: var(--pq-logo-ink);
  transition: var(--pq-transition);
}

.site-logo--invertita .site-logo__word {
  fill: var(--pq-logo-ink-inverted);
}

/*
 * Monocromatica: prende il colore del testo che la circonda. È la variante
 * della stampa, dove il gradiente diventa una macchia grigia, e quella di
 * qualunque contesto a un solo inchiostro.
 */
.site-logo--mono .site-logo__word {
  fill: currentcolor;
}

/*
 * Alla stampa il gradiente non arriva: molti browser scartano gli sfondi e le
 * vernici non piatte, e il simbolo sparirebbe. Il colore del testo attorno,
 * invece, ci arriva sempre: basta chiedere la monocromatica.
 */
@media print {
  .site-logo__word {
    fill: var(--pq-ink-solid);
  }
}
</style>
