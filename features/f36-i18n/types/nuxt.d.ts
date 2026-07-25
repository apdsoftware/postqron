import type { PostqronI18nRuntime } from '../runtime'

declare module '#app' {
  interface NuxtApp {
    $postqronI18n: PostqronI18nRuntime
  }
}

declare module 'vue' {
  interface ComponentCustomProperties {
    $postqronI18n: PostqronI18nRuntime
  }
}

export {}
