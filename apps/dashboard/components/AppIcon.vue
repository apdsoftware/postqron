<script setup lang="ts">
import type { IconDefinition, IconName } from '~/utils/icons'
import { ICONS } from '~/utils/icons'

/**
 * Un'icona del registro in `utils/icons.ts`.
 *
 * Le dimensioni non sono una proprietà: arrivano dalle classi di chi la usa
 * (`class="w-6 h-6"`, come nel template), che Vue inoltra sull'elemento radice.
 * Un'icona che decidesse da sé quanto essere grande costringerebbe a un elenco
 * di taglie da tenere allineato a Tailwind.
 *
 * `aria-hidden` è fisso e non sovrascrivibile: un'icona è decorazione, e il nome
 * accessibile appartiene al controllo che la contiene. Un pulsante di sole icone
 * dichiara quindi il proprio nome con `aria-label` o con uno `<span class="sr-only">`
 * — è la regola che rende navigabile la barra superiore (R54).
 */
const props = defineProps<{ name: IconName }>()

/*
 * L'annotazione allarga il tipo: `ICONS` è dichiarato `as const`, quindi ogni
 * voce ha il proprio tipo letterale e quelle senza `fillRule` non hanno affatto
 * quella proprietà. Serve `as const` — è ciò da cui si ricava `IconName` — e
 * serve poterla leggere qui, dove il nome dell'icona è noto solo a runtime.
 */
const icon = computed<IconDefinition>(() => ICONS[props.name])
</script>

<template>
  <svg
    xmlns="http://www.w3.org/2000/svg"
    viewBox="0 0 20 20"
    fill="currentColor"
    aria-hidden="true"
  >
    <path
      v-for="path in icon.paths"
      :key="path"
      :d="path"
      :fill-rule="icon.fillRule"
      :clip-rule="icon.fillRule"
    />
  </svg>
</template>
