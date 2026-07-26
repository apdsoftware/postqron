<script setup lang="ts" generic="T">
defineProps<{
  items: readonly T[]
  getKey: (item: T) => string
  caption: string
  emptyMessage: string
}>()
</script>

<template>
  <div class="admin-table-wrapper">
    <table class="admin-table">
      <caption class="pq-visually-hidden">
        {{ caption }}
      </caption>
      <thead>
        <tr>
          <slot name="head" />
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="item in items"
          :key="getKey(item)"
        >
          <slot
            name="row"
            :item="item"
          />
        </tr>
      </tbody>
    </table>
    <p
      v-if="items.length === 0"
      class="admin-state"
      role="status"
    >
      {{ emptyMessage }}
    </p>
  </div>
</template>
