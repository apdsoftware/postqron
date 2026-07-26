<script setup lang="ts">
withDefaults(defineProps<{
  caption?: string
  columns: ReadonlyArray<{ key: string, label: string }>
  emptyMessage: string
  rows: ReadonlyArray<Record<string, string>>
}>(), {
  caption: undefined,
})
</script>

<template>
  <div class="admin-table-wrapper">
    <table class="admin-table">
      <caption
        v-if="caption"
        class="pq-visually-hidden"
      >
        {{ caption }}
      </caption>
      <thead>
        <tr>
          <th
            v-for="column in columns"
            :key="column.key"
            scope="col"
          >
            {{ column.label }}
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="rows.length === 0">
          <td :colspan="columns.length">
            {{ emptyMessage }}
          </td>
        </tr>
        <tr
          v-for="(row, index) in rows"
          :key="index"
        >
          <td
            v-for="column in columns"
            :key="column.key"
          >
            <slot
              :name="`cell-${column.key}`"
              :row="row"
              :value="row[column.key]"
            >
              {{ row[column.key] }}
            </slot>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
