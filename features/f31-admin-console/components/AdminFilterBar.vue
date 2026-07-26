<script setup lang="ts">
withDefaults(defineProps<{
  disabled?: boolean
  inputId: string
  label: string
  modelValue: string
  placeholder?: string
  submitLabel: string
}>(), {
  disabled: false,
  placeholder: undefined,
})
const emit = defineEmits<{
  submit: []
  'update:modelValue': [value: string]
}>()

function input(event: unknown): void {
  const value = (event as { target?: { value?: string } }).target?.value ?? ''
  emit('update:modelValue', value)
}
</script>

<template>
  <form
    class="admin-filter-bar"
    @submit.prevent="emit('submit')"
  >
    <label :for="inputId">{{ label }}</label>
    <div>
      <input
        :id="inputId"
        type="search"
        minlength="2"
        maxlength="120"
        :value="modelValue"
        :placeholder="placeholder"
        required
        @input="input"
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
