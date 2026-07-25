# F33 — Support contact

This web slice owns the public `/contatti` route, its complete English,
Italian, Spanish, French, and German catalogs, and the shared public support
contact configuration. The shared composable registers the catalogs lazily
once per i18n runtime, so the page and footer use the same configuration
without a central registry edit.

`NUXT_PUBLIC_SUPPORT_EMAIL` may override the support address. Missing or blank
configuration uses `help@postqron.com`; malformed addresses fail validation
instead of being rendered. The resolved server value is hydrated through Nuxt
state so the address is identical during SSR and on the client.

The page deliberately uses a `mailto` link rather than a form. It therefore
does not collect, log, rate-limit, or persist contact submissions. Normal
requests follow the support-channel runbook objective: acknowledgement within
one business day.
