<script setup lang="ts">
import { reactive, watch } from '#imports'
import type { AdminPlanQuery } from '../core/api.ts'
import {
  localDateTime,
  utcInstant,
} from '../core/list-query.ts'

const props = defineProps<{
  query: AdminPlanQuery
  labels: Readonly<Record<string, string>>
  disabled: boolean
}>()

const emit = defineEmits<{
  apply: [query: AdminPlanQuery]
  reset: []
}>()

const form = reactive({
  q: '',
  plan: '',
  status: '',
  type: '',
  from: '',
  to: '',
})

watch(
  () => props.query,
  (query) => {
    form.q = query.q ?? ''
    form.plan = query.plan ?? ''
    form.status = query.status ?? ''
    form.type = query.type ?? ''
    form.from = localDateTime(query.from)
    form.to = localDateTime(query.to)
  },
  { deep: true, immediate: true },
)

function apply() {
  emit('apply', {
    ...props.query,
    q: form.q.trim(),
    plan: form.plan,
    status: form.status,
    type: form.type,
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
        <span>{{ labels.search }}</span>
        <input
          v-model="form.q"
          type="search"
          maxlength="120"
          :placeholder="labels.searchPlaceholder"
        >
      </label>
      <label>
        <span>{{ labels.plan }}</span>
        <select v-model="form.plan">
          <option value="">
            {{ labels.all }}
          </option>
          <option value="start">Start</option>
          <option value="pro">Pro</option>
          <option value="team">Team</option>
        </select>
      </label>
      <label>
        <span>{{ labels.status }}</span>
        <select v-model="form.status">
          <option value="">
            {{ labels.all }}
          </option>
          <option value="trialing">{{ labels.trialing }}</option>
          <option value="active">{{ labels.active }}</option>
          <option value="past_due">{{ labels.pastDue }}</option>
          <option value="trial_expired">{{ labels.trialExpired }}</option>
          <option value="payment_restricted">{{ labels.restricted }}</option>
          <option value="canceled">{{ labels.canceled }}</option>
        </select>
      </label>
      <label>
        <span>{{ labels.type }}</span>
        <select v-model="form.type">
          <option value="">
            {{ labels.all }}
          </option>
          <option value="public">{{ labels.public }}</option>
          <option value="internal">{{ labels.internal }}</option>
        </select>
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

input,
select {
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
