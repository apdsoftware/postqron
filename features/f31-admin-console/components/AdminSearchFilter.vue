<script setup lang="ts">
const query = defineModel<string>('query', { required: true })

defineProps<{
  label: string
  placeholder: string
  submitLabel: string
  inputId: string
  disabled: boolean
}>()

const emit = defineEmits<{
  submit: []
}>()
</script>

<template>
  <form
    class="admin-search"
    @submit.prevent="emit('submit')"
  >
    <label :for="inputId">{{ label }}</label>
    <div>
      <input
        :id="inputId"
        v-model="query"
        type="search"
        minlength="2"
        maxlength="120"
        :placeholder="placeholder"
        required
      >
      <button
        class="pq-button pq-button--primary"
        type="submit"
        :disabled="disabled"
      >
        {{ submitLabel }}
      </button>
    </div>
  </form>
</template>
