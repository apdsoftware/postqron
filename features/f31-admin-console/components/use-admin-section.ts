import { onScopeDispose, ref, watch, type Ref } from '#imports'
import { normalizeAdminApiError, type AdminApiError } from '../core/api.ts'
import { useAdminSessionState } from '../core/use-admin.ts'

export interface AdminSectionData {
  loading: Ref<boolean>
  errorCode: Ref<AdminApiError['code'] | undefined>
  reload: () => Promise<void>
}

export interface AdminSectionOptions {
  /** Enables controlled background refresh at this interval, in milliseconds. */
  intervalMs?: number
}

const maxPollingBackoffMultiplier = 8

export function useAdminSectionLoad<T>(
  state: Ref<T | undefined>,
  load: (signal: AbortSignal) => Promise<T>,
  options: AdminSectionOptions = {},
): AdminSectionData {
  const session = useAdminSessionState()
  const loading = ref(true)
  const errorCode = ref<AdminApiError['code']>()
  let controller: AbortController | undefined
  let timer: ReturnType<typeof setTimeout> | undefined
  let backoffMs = options.intervalMs ?? 0

  function stopPolling() {
    if (timer !== undefined) {
      clearTimeout(timer)
      timer = undefined
    }
  }

  function schedulePolling() {
    if (!options.intervalMs || !import.meta.client) {
      return
    }
    timer = setTimeout(() => {
      void reload()
    }, backoffMs || options.intervalMs)
  }

  async function reload() {
    controller?.abort()
    stopPolling()
    if (!import.meta.client || !session.value) {
      loading.value = false
      return
    }
    controller = new AbortController()
    const signal = controller.signal
    loading.value = true
    errorCode.value = undefined
    try {
      state.value = await load(signal)
      backoffMs = options.intervalMs ?? 0
    } catch (error) {
      if (signal.aborted) {
        return
      }
      errorCode.value = normalizeAdminApiError(error).code
      if (options.intervalMs) {
        backoffMs = Math.min(
          (backoffMs || options.intervalMs) * 2,
          options.intervalMs * maxPollingBackoffMultiplier,
        )
      }
    } finally {
      if (!signal.aborted) {
        loading.value = false
        schedulePolling()
      }
    }
  }

  watch(session, reload, { immediate: true })

  onScopeDispose(() => {
    controller?.abort()
    stopPolling()
  })

  return { loading, errorCode, reload }
}
