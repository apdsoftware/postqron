<script setup lang="ts">
import {
  definePageMeta,
  navigateTo,
  useRoute,
} from '#imports'
import { legacyProviderManagementRedirect } from '../components/core/navigation.ts'

definePageMeta({ layout: false })

const destination = legacyProviderManagementRedirect(useRoute().fullPath)
if (destination) {
  await navigateTo(destination, { redirectCode: 302, replace: true })
}
</script>

<template>
  <AppState kind="loading" />
</template>
