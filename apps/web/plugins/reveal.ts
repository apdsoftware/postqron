import type { Directive } from 'vue'

import { revealStyle, revealStyleAttribute, type RevealOptions } from '~/utils/reveal'

/**
 * Direttiva `v-reveal`: l'elemento entra in scena quando arriva in vista.
 *
 * Il plugin non è `.client`: la direttiva deve esistere anche in fase di
 * pre-rendering, perché `getSSRProps` scrive nello statico la classe e le
 * variabili dello stato iniziale. Senza, la pagina servita mostrerebbe tutto
 * al suo posto e l'idratazione lo nasconderebbe di colpo.
 */

/** Frazione di elemento visibile che fa scattare la comparsa (viewFactor di scrollReveal). */
const VIEW_FACTOR = 0.2

/**
 * Un observer per elemento, tenuto fuori dal DOM: `unmounted` deve poterlo
 * chiudere anche quando l'elemento è già stato staccato dall'albero.
 */
const observers = new WeakMap<HTMLElement, IntersectionObserver>()

const reveal: Directive<HTMLElement, RevealOptions | undefined> = {
  getSSRProps(binding) {
    return { class: 'pq-reveal', style: revealStyleAttribute(binding.value) }
  },

  mounted(el, binding) {
    el.classList.add('pq-reveal')
    for (const [property, value] of Object.entries(revealStyle(binding.value))) {
      el.style.setProperty(property, value)
    }

    // Browser senza IntersectionObserver: meglio il contenuto senza animazione.
    if (!('IntersectionObserver' in window)) {
      el.classList.add('is-revealed')
      return
    }

    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (!entry.isIntersecting) continue
          entry.target.classList.add('is-revealed')
          observer.unobserve(entry.target)
        }
      },
      { threshold: VIEW_FACTOR },
    )

    observer.observe(el)
    observers.set(el, observer)
  },

  unmounted(el) {
    observers.get(el)?.disconnect()
    observers.delete(el)
  },
}

export default defineNuxtPlugin((nuxtApp) => {
  nuxtApp.vueApp.directive('reveal', reveal)
})
