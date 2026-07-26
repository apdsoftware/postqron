import { setResponseHeader } from 'h3'
import { defineNitroPlugin, useRuntimeConfig } from 'nitropack/runtime'
import { contentSecurityPolicyForHtml } from '../utils/content-security-policy'

export default defineNitroPlugin((nitroApp) => {
  nitroApp.hooks.hook('render:html', (html, { event }) => {
    const config = useRuntimeConfig(event)
    setResponseHeader(
      event,
      'content-security-policy',
      contentSecurityPolicyForHtml([
        ...html.head,
        ...html.bodyPrepend,
        ...html.body,
        ...html.bodyAppend,
      ], config.public.apiBase),
    )
  })
})
