# F36 — Multilingual foundation

This slice provides the reusable English, Italian, Spanish, French, and German
internationalisation foundation. English (`en`) is both the default and the
fallback locale.

## Locale resolution

Resolution is deterministic and has this precedence:

1. a supported locale explicitly present in the URL;
2. the authenticated user's profile preference;
3. the `postqron_locale` preference cookie;
4. `Accept-Language` (including regional tags and quality weights);
5. English.

`resolveLocale` is framework-independent and is shared by the SSR plugin and
tests. The server writes the complete resolution to the Nuxt payload. The
client hydrates that value instead of resolving the browser again, so SSR,
hydration, `html lang`, and the first rendered content agree.

Every canonical localized route has an explicit language prefix: `/en`, `/it`,
`/es`, `/fr`, and `/de`. Legacy unprefixed URLs resolve through the normal
locale precedence and redirect to that canonical form. `localizeUrl` strips an
existing supported prefix before applying the requested one and preserves a
valid path, query, and hash. It accepts origin-relative URLs only, preventing
open redirects and redirect loops.

## Runtime integration

The feature manifest discovers and installs `runtime.ts` as a Nuxt plugin; no
central feature registry is required. It provides:

- `usePostqronI18n()` and `$postqronI18n`;
- a global canonical-locale route middleware;
- the reusable compact native-select `<PostqronLanguageSwitcher />` component;
- catalog registration, translation, pluralisation, and interpolation;
- locale-aware `date`, `number`, `currency`, and `timeZone` formatters.

An authenticated feature registers its profile adapter after loading the
session:

```ts
const i18n = usePostqronI18n()
await i18n.registerProfileStore({
  isAuthenticated: () => true,
  read: () => profile.locale,
  write: locale => updateProfile({ locale }),
})
```

Every manual selection is written to the cookie and, when authenticated, the
registered profile store. Persisted dates, amounts, currency codes, and IANA
time-zone identifiers remain locale-independent; only presentation uses
`Intl`.

## Catalog contract

Each feature owns and registers a namespace with `defineCatalogs`. The English
catalog defines that feature's compile-time key and message-shape contract.
All five locale catalogs are mandatory. `validateCatalogs` rejects missing
keys, orphan keys, changed interpolation placeholders, and HTML-like markup.
Plural messages require `other` and use `Intl.PluralRules`.

Catalog messages contain user-facing copy only. Runtime and technical failures
use the stable `I18nError.code` values and are never translated in place of an
error code. Consumers must render returned translations as text, never with
`v-html`.

## Cookie contract

`LOCALE_COOKIE_CONTRACT` classifies `postqron_locale` as
`necessary_functional`: it is used only to remember an explicit language
choice, requires no optional-cookie consent, contains no personal data, is
site-wide, `SameSite=Lax`, lasts one year, and is `Secure` in production. It is
readable by the application so a manual change can update the hydrated UI.
