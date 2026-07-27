<script setup lang="ts">
import {
  computed,
  definePageMeta,
  useHead,
} from '#imports'
import AdminPageHeader from '../components/AdminPageHeader.vue'
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
  title: t('document.title'),
})))
</script>

<template>
  <section class="admin-page">
    <AdminPageHeader
      :eyebrow="t('page.eyebrow')"
      :title="t('profile.title')"
      :description="t('profile.description')"
    />

    <section
      class="admin-panel"
      aria-labelledby="admin-profile-title"
    >
      <h2 id="admin-profile-title">
        {{ t('shell.profile') }}
      </h2>
      <dl class="admin-profile-details">
        <dt>{{ t('profile.email') }}</dt>
        <dd>{{ session?.account.email }}</dd>
        <dt>{{ t('profile.authenticatedAt') }}</dt>
        <dd>
          <time :datetime="session?.authenticated_at">{{ session ? date(session.authenticated_at) : '' }}</time>
        </dd>
      </dl>
      <p class="admin-profile-hint">
        {{ t('profile.logoutHint') }}
      </p>
    </section>
  </section>
</template>
