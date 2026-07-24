<script setup lang="ts">
import { computed, useAttrs, useId } from 'vue'

defineOptions({ inheritAttrs: false })

const props = withDefaults(defineProps<{
  autocomplete?: string
  disabled?: boolean
  error?: string
  help?: string
  id?: string
  label: string
  modelValue?: string | number
  name?: string
  required?: boolean
  type?: 'text' | 'email' | 'password' | 'search' | 'tel' | 'url'
}>(), {
  autocomplete: undefined,
  disabled: false,
  error: undefined,
  help: undefined,
  id: undefined,
  modelValue: '',
  name: undefined,
  required: false,
  type: 'text',
})

const emit = defineEmits<{
  'update:modelValue': [value: string]
}>()

const attrs = useAttrs()
const generatedId = useId()
const inputId = computed(() => props.id ?? `pq-field-${generatedId}`)
const helpId = computed(() => `${inputId.value}-help`)
const errorId = computed(() => `${inputId.value}-error`)
const describedBy = computed(() => [
  props.help ? helpId.value : undefined,
  props.error ? errorId.value : undefined,
].filter(Boolean).join(' ') || undefined)

function updateValue(event: unknown): void {
  const target = (event as { target?: { value?: string } }).target
  emit('update:modelValue', target?.value ?? '')
}
</script>

<template>
  <div
    class="pq-field"
    :data-invalid="Boolean(error) || undefined"
  >
    <label
      class="pq-field__label"
      :for="inputId"
    >
      {{ label }}
      <span
        v-if="required"
        class="pq-field__required"
        aria-hidden="true"
      >*</span>
      <span
        v-if="required"
        class="pq-visually-hidden"
      >(obbligatorio)</span>
    </label>
    <input
      v-bind="attrs"
      :id="inputId"
      class="pq-field__input"
      :name="name"
      :type="type"
      :value="modelValue"
      :autocomplete="autocomplete"
      :disabled="disabled"
      :required="required"
      :aria-invalid="error ? 'true' : undefined"
      :aria-describedby="describedBy"
      @input="updateValue"
    >
    <p
      v-if="help"
      :id="helpId"
      class="pq-field__help"
    >
      {{ help }}
    </p>
    <p
      v-if="error"
      :id="errorId"
      class="pq-field__error"
      role="alert"
    >
      {{ error }}
    </p>
  </div>
</template>
