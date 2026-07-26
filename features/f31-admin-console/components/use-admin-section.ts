import { ref, watch, type Ref } from '#imports'
import { normalizeAdminApiError, type AdminApiError } from '../core/api.ts'
import { useAdminSessionState } from '../core/use-admin.ts'

export interface AdminSectionData {
  loading: Ref<boolean>
  errorCode: Ref<AdminApiError['code'] | undefined>
  reload: () => Promise<void>
}

export function useAdminSectionLoad<T>(
  state: Ref<T | undefined>,
  load: () => Promise<T>,
): AdminSectionData {
  const session = useAdminSessionState()
  const loading = ref(true)
  const errorCode = ref<AdminApiError['code']>()

  async function reload() {
    if (!import.meta.client || !session.value) {
      loading.value = false
      return
    }
    loading.value = true
    errorCode.value = undefined
    try {
      state.value = await load()
    } catch (error) {
      errorCode.value = normalizeAdminApiError(error).code
    } finally {
      loading.value = false
    }
  }

  watch(session, reload, { immediate: true })

  return { loading, errorCode, reload }
}
