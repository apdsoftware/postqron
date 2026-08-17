<script setup lang="ts">
import heroHexagons from '~/assets/images/hero-hexagons.svg'

defineProps<{
  title: string
  text: string
  image: string
  imageAlt: string
  /** Nota sotto al campo email. */
  note: string
  /** Video di presentazione; se manca, il pulsante di riproduzione non compare. */
  video?: { href: string, embedSrc: string, title: string }
}>()

const email = ref('')

/*
 * L'iscrizione arriverà con l'API (#396): per ora il campo valida il formato e
 * si ferma lì, senza far credere a un invio che non avviene.
 */
function onSubmit() {
  console.info('Iscrizione richiesta per', email.value)
}
</script>

<template>
  <div
    id="welcome"
    class="hero"
  >
    <div class="hero__photo">
      <img
        :src="image"
        :alt="imageAlt"
        width="1065"
        height="955"
        fetchpriority="high"
      >
    </div>

    <!--
      La sagoma esagonale è ritagliata nel colore della pagina e appoggiata
      sopra la foto: è lei a produrre il taglio obliquo fra testo e immagine.
    -->
    <div class="hero__cutout">
      <img
        :src="heroHexagons"
        alt=""
        width="1920"
        height="960"
      >
    </div>

    <div class="hero__text">
      <div class="container">
        <div class="row">
          <div class="col-lg-5 col-md-12 col-sm-12">
            <h1 class="hero__title">
              {{ title }}
            </h1>
            <p class="hero__lead">
              {{ text }}
            </p>
            <EmailSignup
              v-model="email"
              :note="note"
              @submit="onSubmit"
            />
          </div>
        </div>
      </div>
    </div>

    <div
      v-if="video"
      class="hero__play"
    >
      <VideoDialog v-bind="video" />
    </div>
  </div>
</template>

<style scoped>
.hero {
  position: relative;
  overflow: hidden;

  /* Isola gli strati dell'hero: il ritaglio non deve competere con l'header. */
  transform-style: preserve-3d;
}

.hero__photo {
  position: relative;
  float: right;
  width: 55.5%;
  z-index: 1;
  overflow: hidden;
}

.hero__photo img {
  display: block;
  max-width: 100%;
  height: auto;
}

/* Velo a gradiente sulla foto, a metà opacità. */
.hero__photo::before {
  content: '';
  position: absolute;
  inset: 0;
  opacity: 0.5;
  background-image: var(--pq-gradient);
}

.hero__cutout {
  position: absolute;
  top: 0;
  width: 100%;
  min-height: 500px;
  z-index: 2;
}

.hero__cutout img {
  display: block;
  width: 100%;
  height: auto;
}

.hero__text {
  position: absolute;
  top: 50%;
  width: 100%;
  z-index: 4;
  transform: perspective(1px) translateY(-50%);
}

.hero__title {
  margin-bottom: 40px;
  color: var(--pq-heading-hero);
  font-size: 42px;
  font-weight: 400;
  letter-spacing: 1.4px;
  line-height: 54px;
}

.hero__lead {
  margin-bottom: 40px;
  color: var(--pq-text-hero);
  font-size: 16px;
  letter-spacing: 1px;
  line-height: 28px;
}

.hero__play {
  position: absolute;
  top: 45%;
  right: 0;
  width: 55.5%;
  z-index: 4;
  transform: perspective(1px) translateY(-45%);
}

/* Il pulsante è centrato nella metà destra, spostato per stare nell'esagono. */
.hero__play :deep(.video-dialog) {
  position: absolute;
  top: 50%;
  right: 0;
  left: 10%;
  width: 60px;
  margin: auto;
  transform: perspective(1px) translateY(-50%);
}

@media (max-width: 1200px) {
  .hero__text {
    top: 60%;
    transform: perspective(1px) translateY(-60%);
  }

  .hero__title {
    font-size: 32px;
    line-height: 42px;
  }
}

@media (max-width: 991px) {
  /* Sotto l'header, che qui è sempre una barra bianca alta 80px. */
  .hero {
    margin-top: var(--pq-header-height-compact);
  }

  .hero__photo {
    width: 100%;
  }

  .hero__cutout,
  .hero__play {
    display: none;
  }

  /* Il testo passa sopra la foto: serve il contrasto del bianco. */
  .hero__title {
    margin-bottom: 10px;
    color: #fff;
    font-size: 22px;
    font-weight: 500;
    line-height: 32px;
    text-align: center;
  }

  .hero__lead {
    color: #fff;
    text-align: center;
  }
}
</style>
