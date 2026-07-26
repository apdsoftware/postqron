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
  title: `${t('workspaces.page.title')} — Postqron`,
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
      :eyebrow="t('workspaces.page.eyebrow')"
      :title="t('workspaces.page.title')"
      :description="t('workspaces.page.description')"
    />

    <AdminLoginGate v-if="!session" />

    <section
      v-else
      class="admin-panel"
      aria-labelledby="admin-workspaces-title"
    >
      <h2 id="admin-workspaces-title">
        {{ t('workspaces.page.title') }}
      </h2>
      <AdminFilterBar
        input-id="admin-workspaces-search"
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
        :message="t('workspaces.prompt')"
      />
      <AdminDataTable
        v-else
        :caption="t('workspaces.page.title')"
        :columns="[
          { key: 'name', label: t('workspaces.table.name') },
          { key: 'owner', label: t('workspaces.table.owner') },
          { key: 'members', label: t('workspaces.table.members') },
        ]"
        :rows="searchResults.workspaces.map(workspace => ({
          name: workspace.name,
          owner: workspace.owner_email,
          members: String(workspace.member_count),
        }))"
        :empty-message="t('status.empty')"
      />
    </section>
  </section>
</template>
