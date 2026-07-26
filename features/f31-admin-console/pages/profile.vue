<script setup lang="ts">
import { computed, definePageMeta, useHead } from '#imports'
import {
  useAdminI18n,
  useAdminSessionState,
} from '../core/use-admin.ts'

definePageMeta({
  layout: 'admin-console',
  middleware: 'admin-access',
})

const session = useAdminSessionState()
const { date, t } = useAdminI18n()

useHead(computed(() => ({
  title: `${t('profile.page.title')} — Postqron`,
})))
</script>

<template>
  <section class="admin-page">
    <AdminPageHeader
      :eyebrow="t('profile.page.eyebrow')"
      :title="t('profile.page.title')"
      :description="t('profile.page.description')"
    />

    <AdminLoginGate v-if="!session" />

    <section
      v-else
      class="admin-panel"
      aria-labelledby="admin-profile-title"
    >
      <h2 id="admin-profile-title">
        {{ t('profile.page.title') }}
      </h2>
      <dl class="admin-profile-summary">
        <div>
          <dt>{{ t('profile.account.id') }}</dt>
          <dd>{{ session.account.id }}</dd>
        </div>
        <div>
          <dt>{{ t('profile.account.email') }}</dt>
          <dd>{{ session.account.email }}</dd>
        </div>
        <div>
          <dt>{{ t('profile.account.authenticatedAt') }}</dt>
          <dd>
            <time :datetime="session.authenticated_at">{{ date(session.authenticated_at) }}</time>
          </dd>
        </div>
      </dl>
    </section>

    <AdminAlert
      v-if="session"
      variant="info"
      :title="t('profile.logout.title')"
    >
      {{ t('profile.logout.description') }}
    </AdminAlert>
  </section>
</template>
