<script setup lang="ts">
withDefaults(defineProps<{
  compact?: boolean
  reversed?: boolean
}>(), {
  compact: false,
  reversed: false,
})
</script>

<template>
  <svg
    class="pq-logo"
    :class="{ 'pq-logo--compact': compact, 'pq-logo--reversed': reversed }"
    :viewBox="compact ? '0 0 64 64' : '0 0 266 64'"
    role="img"
    aria-label="Postqron"
  >
    <g>
      <rect
        x="6"
        y="6"
        width="48"
        height="48"
        rx="15"
        :fill="reversed ? '#ffffff' : 'var(--pq-color-brand, #185c43)'"
      />
      <circle
        cx="30"
        cy="29"
        r="11"
        fill="none"
        :stroke="reversed ? '#185c43' : '#ffffff'"
        stroke-width="4"
      />
      <path
        d="M30 22v7l5 3"
        fill="none"
        :stroke="reversed ? '#185c43' : '#ffffff'"
        stroke-width="4"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
      <path
        d="m39 39 9 9"
        fill="none"
        :stroke="reversed ? '#ffffff' : 'var(--pq-color-brand, #185c43)'"
        stroke-width="8"
        stroke-linecap="round"
      />
      <path
        d="m39 39 9 9"
        fill="none"
        :stroke="reversed ? '#185c43' : '#ffffff'"
        stroke-width="4"
        stroke-linecap="round"
      />
      <circle
        cx="50"
        cy="14"
        r="6"
        :fill="reversed ? '#ffa17e' : 'var(--pq-color-accent, #c43d12)'"
      />
    </g>
    <text
      v-if="!compact"
      x="72"
      y="42"
      :fill="reversed ? '#ffffff' : 'var(--pq-color-text, #13231d)'"
      font-family="Inter, Arial, sans-serif"
      font-size="35"
      font-weight="750"
      letter-spacing="-1.2"
    >Postqron</text>
  </svg>
</template>
