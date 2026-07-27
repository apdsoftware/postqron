<script setup lang="ts">
import { reactive, watch } from '#imports'
import type { AdminAuditQuery } from '../core/api.ts'
import {
  localDateTime,
  utcInstant,
} from '../core/list-query.ts'

const props = defineProps<{
  query: AdminAuditQuery
  labels: Readonly<Record<string, string>>
  disabled: boolean
}>()

const emit = defineEmits<{
  apply: [query: AdminAuditQuery]
  reset: []
}>()

const form = reactive({
  action: '',
  actor: '',
  subject: '',
  outcome: '',
  from: '',
  to: '',
})

watch(
  () => props.query,
  (query) => {
    form.action = query.action ?? ''
    form.actor = query.actor ?? ''
    form.subject = query.subject ?? ''
    form.outcome = query.outcome ?? ''
    form.from = localDateTime(query.from)
    form.to = localDateTime(query.to)
  },
  { deep: true, immediate: true },
)

function apply() {
  emit('apply', {
    ...props.query,
    action: form.action.trim(),
    actor: form.actor.trim(),
    subject: form.subject.trim(),
    outcome: form.outcome.trim(),
    from: utcInstant(form.from),
    to: utcInstant(form.to),
    page: 1,
  })
}
</script>

<template>
  <form
    class="admin-data-filters"
    @submit.prevent="apply"
  >
    <div class="admin-data-filters__grid">
      <label>
        <span>{{ labels.action }}</span>
        <input
          v-model="form.action"
          maxlength="128"
          :placeholder="labels.actionPlaceholder"
        >
      </label>
      <label>
        <span>{{ labels.actor }}</span>
        <input
          v-model="form.actor"
          maxlength="128"
          :placeholder="labels.actorPlaceholder"
        >
      </label>
      <label>
        <span>{{ labels.subject }}</span>
        <input
          v-model="form.subject"
          maxlength="128"
          :placeholder="labels.subjectPlaceholder"
        >
      </label>
      <label>
        <span>{{ labels.outcome }}</span>
        <input
          v-model="form.outcome"
          maxlength="64"
          :placeholder="labels.outcomePlaceholder"
        >
      </label>
      <label>
        <span>{{ labels.from }}</span>
        <input
          v-model="form.from"
          type="datetime-local"
        >
      </label>
      <label>
        <span>{{ labels.to }}</span>
        <input
          v-model="form.to"
          type="datetime-local"
        >
      </label>
    </div>
    <p class="admin-data-filters__timezone">
      {{ labels.timezone }}
    </p>
    <div class="admin-data-filters__actions">
      <button
        class="pq-button pq-button--secondary"
        type="button"
        :disabled="disabled"
        @click="emit('reset')"
      >
        {{ labels.reset }}
      </button>
      <button
        class="pq-button pq-button--primary"
        type="submit"
        :disabled="disabled"
      >
        {{ labels.apply }}
      </button>
    </div>
  </form>
</template>

<style scoped>
.admin-data-filters {
  display: grid;
  gap: var(--pq-space-3);
  border: 1px solid var(--pq-color-border);
  border-radius: var(--pq-radius-lg);
  padding: var(--pq-space-4);
  background: var(--pq-color-surface);
}

.admin-data-filters__grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 12rem), 1fr));
  gap: var(--pq-space-3);
}

label {
  display: grid;
  gap: var(--pq-space-2);
  min-width: 0;
  color: var(--pq-color-text-muted);
  font-size: var(--pq-font-size-sm);
  font-weight: var(--pq-font-weight-semibold);
}

input {
  width: 100%;
  min-height: 2.75rem;
  border: 1px solid var(--pq-color-border-strong);
  border-radius: var(--pq-radius-md);
  padding: var(--pq-space-2) var(--pq-space-3);
  color: var(--pq-color-text);
  background: var(--pq-color-surface);
}

.admin-data-filters__timezone {
  margin: 0;
  color: var(--pq-color-text-muted);
  font-size: var(--pq-font-size-sm);
}

.admin-data-filters__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--pq-space-3);
}
</style>
