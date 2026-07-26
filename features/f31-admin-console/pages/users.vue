<script setup lang="ts">
import { computed, definePageMeta, ref, useHead } from '#imports'
import { AdminApiError, normalizeAdminApiError } from '../core/api.ts'
import {
  useAdminApi,
  useAdminI18n,
  useAdminSearchState,
  useAdminSessionState,
} from '../core/use-admin.ts'

definePageMeta({
  layout: 'admin-console',
  middleware: 'admin-access',
})

const api = useAdminApi()
const session = useAdminSessionState()
const searchResults = useAdminSearchState()
const { t } = useAdminI18n()

useHead(computed(() => ({
  title: `${t('users.page.title')} — Postqron`,
})))

const searching = ref(false)
const query = ref('')
const errorCode = ref<AdminApiError['code']>()

async function search() {
  searching.value = true
  errorCode.value = undefined
  try {
    searchResults.value = await api.search(query.value)
  } catch (error) {
    errorCode.value = normalizeAdminApiError(error).code
  } finally {
    searching.value = false
  }
}
</script>

<template>
  <section class="admin-page">
    <AdminPageHeader
      :eyebrow="t('users.page.eyebrow')"
      :title="t('users.page.title')"
      :description="t('users.page.description')"
    />

    <AdminLoginGate v-if="!session" />

    <section
      v-else
      class="admin-panel"
      aria-labelledby="admin-users-title"
    >
      <h2 id="admin-users-title">
        {{ t('users.page.title') }}
      </h2>
      <AdminFilterBar
        input-id="admin-users-search"
        :label="t('search.label')"
        :model-value="query"
        :placeholder="t('search.placeholder')"
        :submit-label="t('search.submit')"
        :disabled="searching"
        @update:model-value="query = $event"
        @submit="search"
      />
      <p
        v-if="errorCode"
        class="admin-inline-error"
        role="alert"
      >
        {{ t(`error.${errorCode}` as never) }}
      </p>
      <AdminState
        v-if="!searchResults"
        variant="empty"
        :message="t('users.prompt')"
      />
      <AdminDataTable
        v-else
        :caption="t('users.page.title')"
        :columns="[
          { key: 'name', label: t('users.table.name') },
          { key: 'email', label: t('users.table.email') },
          { key: 'status', label: t('users.table.status') },
        ]"
        :rows="searchResults.users.map(user => ({
          name: user.display_name,
          email: user.email,
          status: user.email_verified ? t('search.verified') : t('search.unverified'),
        }))"
        :empty-message="t('status.empty')"
      />
    </section>
  </section>
</template>
