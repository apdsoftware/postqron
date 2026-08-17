<script setup lang="ts">
const props = defineProps<{
  /** URL della pagina del video: è il fallback quando la finestra non si apre. */
  href: string
  /** URL da incorporare nell'iframe. */
  embedSrc: string
  /** Titolo del video, letto dagli screen reader e usato come `title` dell'iframe. */
  title: string
  /** Etichetta del pulsante di chiusura: arriva da `ui.closeVideo`. */
  closeLabel: string
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
        {{ closeLabel }}
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
  border: 1px solid var(--pq-text-inverted);
  border-radius: var(--pq-radius-pill);
  color: var(--pq-text-inverted);
  font-size: var(--pq-text-xl);
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
  background: var(--pq-surface);
}

.video-dialog__glyph {
  position: relative;
  z-index: 2;

  /* Il triangolo è otticamente decentrato: va spostato a destra. */
  margin-left: var(--pq-space-1);
}

.video-dialog__modal {
  width: min(960px, 90vw);
  padding: 0;
  border: none;
  border-radius: var(--pq-radius);
  background: var(--pq-ink-solid);
}

.video-dialog__modal::backdrop {
  background: var(--pq-scrim);
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
  color: var(--pq-text-inverted);
  font-size: var(--pq-text-2xs);
  font-weight: var(--pq-weight-bold);
  letter-spacing: 0.75px;
  text-transform: uppercase;
  cursor: pointer;
}
</style>
