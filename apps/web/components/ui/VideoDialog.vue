<script setup lang="ts">
const props = defineProps<{
  /** URL della pagina del video: è il fallback quando la finestra non si apre. */
  href: string
  /** URL da incorporare nell'iframe. */
  embedSrc: string
  /** Titolo del video, letto dagli screen reader e usato come `title` dell'iframe. */
  title: string
}>()

/** Sotto questa larghezza il tema rinuncia alla lightbox e segue il link. */
const DIALOG_MIN_WIDTH = 700

const dialog = useTemplateRef<HTMLDialogElement>('dialog')
const isOpen = ref(false)

/*
 * L'iframe viene creato solo all'apertura: finché non lo si chiede, la pagina
 * non contatta il servizio esterno e non deposita nulla nel browser. Serve
 * anche a non far pesare un embed di terze parti sul primo caricamento.
 */
function open(event: MouseEvent) {
  if (window.innerWidth < DIALOG_MIN_WIDTH || typeof dialog.value?.showModal !== 'function') {
    return
  }

  event.preventDefault()
  isOpen.value = true
  dialog.value.showModal()
}

function close() {
  isOpen.value = false
  dialog.value?.close()
}
</script>

<template>
  <div class="video-dialog">
    <a
      :href="props.href"
      class="video-dialog__button"
      target="_blank"
      rel="noopener"
      @click="open"
    >
      <HexIcon
        name="play"
        :label="title"
        class="video-dialog__glyph"
      />
    </a>

    <!-- `close` copre anche il tasto Esc, che il browser gestisce da sé. -->
    <dialog
      ref="dialog"
      class="video-dialog__modal"
      @close="close"
    >
      <button
        type="button"
        class="video-dialog__close"
        @click="close"
      >
        Chiudi
      </button>
      <iframe
        v-if="isOpen"
        :src="embedSrc"
        :title="title"
        allow="accelerometer; autoplay; clipboard-write; encrypted-media; picture-in-picture"
        allowfullscreen
      />
    </dialog>
  </div>
</template>

<style scoped>
.video-dialog__button {
  display: block;
  position: relative;
  width: 60px;
  height: 60px;
  overflow: hidden;
  border: 1px solid #fff;
  border-radius: var(--pq-radius-pill);
  color: #fff;
  font-size: 22px;
  line-height: 60px;
  text-align: center;
}

/* Velo bianco al 19%: il vetro smerigliato del tema. */
.video-dialog__button::before {
  content: '';
  position: absolute;
  inset: 0;
  z-index: 1;
  opacity: 0.19;
  background: #fff;
}

.video-dialog__glyph {
  position: relative;
  z-index: 2;

  /* Il triangolo è otticamente decentrato: va spostato a destra. */
  margin-left: 5px;
}

.video-dialog__modal {
  width: min(960px, 90vw);
  padding: 0;
  border: none;
  border-radius: var(--pq-radius);
  background: #000;
}

.video-dialog__modal::backdrop {
  background: rgb(11 17 32 / 80%);
}

.video-dialog__modal iframe {
  display: block;
  width: 100%;
  aspect-ratio: 16 / 9;
  border: 0;
}

.video-dialog__close {
  position: absolute;
  top: -34px;
  right: 0;
  padding: 0;
  border: 0;
  background: none;
  color: #fff;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.75px;
  text-transform: uppercase;
  cursor: pointer;
}
</style>
