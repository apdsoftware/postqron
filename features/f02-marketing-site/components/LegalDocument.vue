<script setup lang="ts">
import type { PublishedLegalDocument } from '@postqron/compliance'
import { computed } from 'vue'
import { toLegalBlocks } from '~/src/legal'

const props = defineProps<{
  document: PublishedLegalDocument
}>()
const blocks = computed(() => toLegalBlocks(props.document.content))
</script>

<template>
  <div class="legal-document">
    <template
      v-for="(block, index) in blocks"
      :key="index"
    >
      <h2 v-if="block.kind === 'heading' && block.level === 2">
        {{ block.text }}
      </h2>
      <h3 v-else-if="block.kind === 'heading'">
        {{ block.text }}
      </h3>
      <p v-else-if="block.kind === 'paragraph'">
        {{ block.text }}
      </p>
      <ul v-else>
        <li
          v-for="item in block.items"
          :key="item"
        >
          {{ item }}
        </li>
      </ul>
    </template>
  </div>
</template>
