<script setup lang="ts">
import { countUpValue, formatCount } from '~/utils/countUp'

const props = withDefaults(
  defineProps<{
    /** Valore finale del contatore. */
    value: number
    /** Etichetta sotto al numero; `\n` va a capo. */
    label: string
    /** Durata del conteggio, in millisecondi. */
    duration?: number
  }>(),
  { duration: 1000 },
)

const root = useTemplateRef<HTMLElement>('root')

// Il valore parte già completo: è quello che finisce nell'HTML pre-renderizzato.
const displayed = ref(props.value)

/**
 * Il conteggio parte quando la colonna entra in vista, come faceva counterUp.
 * Il valore mostrato lato server è già quello finale: chi legge senza
 * JavaScript, o con le animazioni disattivate, vede il numero e non uno zero.
 */
onMounted(() => {
  const element = root.value
  if (!element) return

  const prefersStill = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  if (prefersStill || !('IntersectionObserver' in window)) {
    displayed.value = props.value
    return
  }

  displayed.value = 0
  let frame = 0

  const animate = () => {
    const start = performance.now()
    const step = (now: number) => {
      const progress = Math.min((now - start) / props.duration, 1)
      displayed.value = countUpValue(props.value, progress)
      if (progress < 1) frame = requestAnimationFrame(step)
    }
    frame = requestAnimationFrame(step)
  }

  const observer = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue
        observer.unobserve(entry.target)
        animate()
      }
    },
    { threshold: 0.2 },
  )
  observer.observe(element)

  onBeforeUnmount(() => {
    observer.disconnect()
    cancelAnimationFrame(frame)
  })
})
</script>

<template>
  <div
    ref="root"
    class="stat"
  >
    <strong class="stat__value">{{ formatCount(displayed) }}</strong>
    <span class="stat__label">
      <template
        v-for="(line, index) in label.split('\n')"
        :key="line"
      >
        <br v-if="index > 0">{{ line }}
      </template>
    </span>
  </div>
</template>

<style scoped>
.stat {
  position: relative;
  height: 280px;
  overflow: hidden;
}

.stat__value {
  display: block;
  margin-top: 70px;
  margin-bottom: 10px;
  color: #fff;
  font-size: 40px;
  font-weight: 400;
  letter-spacing: 1.72px;
  text-align: center;
  transition: var(--pq-transition);
}

/* Al passaggio del mouse il numero scende: è l'unico movimento della fascia. */
.stat:hover .stat__value {
  margin-top: 60px;
}

.stat__label {
  display: block;
  color: #fff;
  font-size: 20px;
  letter-spacing: 0.86px;
  text-align: center;
}

@media (max-width: 991px) {
  .stat {
    height: auto;
    padding-top: 20px;
    padding-bottom: 20px;
  }

  .stat__value,
  .stat:hover .stat__value {
    margin-top: 0;
  }
}
</style>
