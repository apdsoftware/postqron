import { onNuxtReady } from '#app'
import { PostqronPWA } from '../../../features/f23-pwa/web/pwa-client.mjs'

export default defineNuxtPlugin(() => {
  const pwa = new PostqronPWA()
  onNuxtReady(() => {
    void pwa.register().catch(() => undefined)
  })
  return {
    provide: {
      pwa,
    },
  }
})
