export * from './api.ts'
export * from './bundle.ts'
export * from './markdown.ts'
export * from './repository.ts'
export * from './types.ts'
export * from './validation.ts'

// `./content.ts` is deliberately NOT re-exported here. It uses Node's
// `node:fs`/`node:path`/`node:url` to load the committed draft corpus for
// tests only. `src/index.ts` is imported by the browser-bundled Vue page
// (`pages/legal-document.vue`), and bundling those Node built-ins into the
// client breaks the Nuxt/Vite build. Import `./content.ts` directly from
// test files instead.
