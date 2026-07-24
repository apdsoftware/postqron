<script setup lang="ts">
withDefaults(defineProps<{
  href?: string
  label?: string
}>(), {
  href: '#main-content',
  label: 'Vai al contenuto principale',
})
</script>

<template>
  <a
    class="pq-skip-link"
    :href="href"
  >{{ label }}</a>
</template>
