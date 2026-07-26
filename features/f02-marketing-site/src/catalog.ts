export const PRICING_LOCALES = ['en', 'it', 'es', 'fr', 'de'] as const

export type PricingLocale = typeof PRICING_LOCALES[number]
export type BillingInterval = 'monthly' | 'annual'
export type PublicPlanCode = 'start' | 'pro' | 'team'

export interface Money {
  amount_cents: number
  currency: 'EUR'
}

export interface PriceTier {
  from_channel: number
  to_channel: number
  monthly: Money
  annual: Money
}

export interface PublicPlan {
  code: PublicPlanCode
  name: string
  purchasable: boolean
  prices: Record<BillingInterval, Money>
  price_tiers: PriceTier[]
  limits: {
    members: number
    channels: number
    scheduled_publications: number
    scheduled_publications_per_channel: number
  }
  trial?: {
    days: number
    members: number
    channels: number
    scheduled_publications_per_channel: number
  }
}

export interface PublicCatalog {
  provider: 'paddle'
  catalog_version: 'd07-v1'
  currency: 'EUR'
  plans: PublicPlan[]
}

export interface PricingCopy {
  seoTitle: string
  seoDescription: string
  eyebrow: string
  heroTitle: string
  heroDescription: string
  loading: string
  unavailable: string
  unavailableDetail: string
  retry: string
  includedTitle: string
  includedCalendar: string
  includedDrafts: string
  includedStatus: string
  includedPrivacy: string
  faqPrompt: string
  faqLink: string
  intervalLabel: string
  monthly: string
  annual: string
  annualBadge: string
  quantityLabel: string
  quantityHelp: string
  featured: string
  perMonth: string
  perYear: string
  monthlyBilling: string
  annualBilling: string
  annualEquivalent: string
  freeForever: string
  choosePlan: string
  chooseFree: string
  member: string
  members: string
  channel: string
  channels: string
  scheduledPerChannel: string
  trial: string
  taxNotice: string
  benchmarkTitle: string
  benchmarkIntro: string
  benchmarkPlan: string
  postqron: string
  buffer: string
  saving: string
  comparisonScope: string
  comparisonLimits: string
}

export const PRICING_COPY: Readonly<Record<PricingLocale, PricingCopy>> = {
  en: {
    seoTitle: 'Pricing and plans — Postqron',
    seoDescription: 'Compare Start, Pro, and Team with progressive pricing, clear limits, and a transparent Buffer benchmark.',
    eyebrow: 'Pricing',
    heroTitle: 'A clear price for every channel.',
    heroDescription: 'Choose monthly or annual billing, set the number of channels, and see the complete recurring total before checkout.',
    loading: 'Loading plans…',
    unavailable: 'Pricing is temporarily unavailable',
    unavailableDetail: 'We do not show stale fallback prices. Try again to load the official server catalog.',
    retry: 'Try again',
    includedTitle: 'Every plan includes',
    includedCalendar: 'Editorial calendar',
    includedDrafts: 'Drafts and scheduling',
    includedStatus: 'Publishing status',
    includedPrivacy: 'Privacy and security controls',
    faqPrompt: 'Still unsure?',
    faqLink: 'Read the frequently asked questions.',
    intervalLabel: 'Billing frequency',
    monthly: 'Monthly',
    annual: 'Annual',
    annualBadge: '2 months free',
    quantityLabel: 'Social channels',
    quantityHelp: 'The paid-plan total updates for 1 to 50 channels.',
    featured: 'Most popular',
    perMonth: '/month',
    perYear: '/year',
    monthlyBilling: 'Billed monthly',
    annualBilling: '{total} billed once a year',
    annualEquivalent: 'Equivalent to {monthly}/month',
    freeForever: 'Free forever. No payment method.',
    choosePlan: 'Choose {plan}',
    chooseFree: 'Start free',
    member: '{count} user',
    members: '{count} users',
    channel: '{count} social channel',
    channels: '{count} social channels',
    scheduledPerChannel: '{count} scheduled posts per channel',
    trial: 'One 14-day Team trial, no card required',
    taxNotice: 'Base catalog in EUR. Transaction taxes are calculated by Paddle for your location and shown before consent. Paddle is the Merchant of Record.',
    benchmarkTitle: 'Transparent Buffer benchmark',
    benchmarkIntro: 'D07 benchmark captured 24 July 2026. It compares the same channel quantity and billing period, before transaction taxes.',
    benchmarkPlan: 'Equivalent plan',
    postqron: 'Postqron total',
    buffer: 'Buffer converted to EUR',
    saving: 'You save {amount}',
    comparisonScope: 'Pro is compared with Buffer Essentials; Team with Buffer Team. Start matches Buffer Free up to 3 channels.',
    comparisonLimits: 'This is a commercial comparison, not feature parity: at launch Postqron supports fewer social networks, 500 scheduled posts per channel, up to 15 Team users, and does not include Buffer analytics, inbox, AI, API, or approval workflows.',
  },
  it: {
    seoTitle: 'Prezzi e piani — Postqron',
    seoDescription: 'Confronta Start, Pro e Team con prezzi progressivi, limiti chiari e un benchmark trasparente con Buffer.',
    eyebrow: 'Prezzi',
    heroTitle: 'Un prezzo chiaro per ogni canale.',
    heroDescription: 'Scegli mensile o annuale, imposta il numero di canali e consulta il totale ricorrente completo prima del checkout.',
    loading: 'Caricamento dei piani…',
    unavailable: 'Prezzi momentaneamente non disponibili',
    unavailableDetail: 'Non mostriamo prezzi di ripiego non aggiornati. Riprova per caricare il catalogo ufficiale del server.',
    retry: 'Riprova',
    includedTitle: 'Tutti i piani includono',
    includedCalendar: 'Calendario editoriale',
    includedDrafts: 'Bozze e programmazione',
    includedStatus: 'Stato delle pubblicazioni',
    includedPrivacy: 'Controlli privacy e sicurezza',
    faqPrompt: 'Hai ancora dubbi?',
    faqLink: 'Consulta le domande frequenti.',
    intervalLabel: 'Frequenza di fatturazione',
    monthly: 'Mensile',
    annual: 'Annuale',
    annualBadge: '2 mesi gratis',
    quantityLabel: 'Canali social',
    quantityHelp: 'Il totale dei piani a pagamento si aggiorna da 1 a 50 canali.',
    featured: 'Più scelto',
    perMonth: '/mese',
    perYear: '/anno',
    monthlyBilling: 'Fatturazione mensile',
    annualBilling: '{total} fatturati una volta all’anno',
    annualEquivalent: 'Equivale a {monthly}/mese',
    freeForever: 'Gratis per sempre. Nessun metodo di pagamento.',
    choosePlan: 'Scegli {plan}',
    chooseFree: 'Inizia gratis',
    member: '{count} utente',
    members: '{count} utenti',
    channel: '{count} canale social',
    channels: '{count} canali social',
    scheduledPerChannel: '{count} post programmati per canale',
    trial: 'Una prova Team di 14 giorni, senza carta',
    taxNotice: 'Catalogo base in EUR. Paddle calcola le imposte della transazione per la tua località e le mostra prima del consenso. Paddle è il Merchant of Record.',
    benchmarkTitle: 'Confronto trasparente con Buffer',
    benchmarkIntro: 'Benchmark D07 rilevato il 24 luglio 2026. Confronta lo stesso numero di canali e periodo, prima delle imposte di transazione.',
    benchmarkPlan: 'Piano equivalente',
    postqron: 'Totale Postqron',
    buffer: 'Buffer convertito in EUR',
    saving: 'Risparmi {amount}',
    comparisonScope: 'Pro è confrontato con Buffer Essentials; Team con Buffer Team. Start è pari a Buffer Free fino a 3 canali.',
    comparisonLimits: 'È un confronto commerciale, non una parità di funzioni: al lancio Postqron supporta meno social, 500 post programmati per canale, fino a 15 utenti Team e non include analytics, inbox, AI, API o approvazioni di Buffer.',
  },
  es: {
    seoTitle: 'Precios y planes — Postqron',
    seoDescription: 'Compara Start, Pro y Team con precios progresivos, límites claros y una referencia transparente de Buffer.',
    eyebrow: 'Precios',
    heroTitle: 'Un precio claro para cada canal.',
    heroDescription: 'Elige facturación mensual o anual, fija el número de canales y consulta el total recurrente completo antes del pago.',
    loading: 'Cargando planes…',
    unavailable: 'Los precios no están disponibles temporalmente',
    unavailableDetail: 'No mostramos precios alternativos desactualizados. Vuelve a intentar cargar el catálogo oficial del servidor.',
    retry: 'Reintentar',
    includedTitle: 'Todos los planes incluyen',
    includedCalendar: 'Calendario editorial',
    includedDrafts: 'Borradores y programación',
    includedStatus: 'Estado de las publicaciones',
    includedPrivacy: 'Controles de privacidad y seguridad',
    faqPrompt: '¿Aún tienes dudas?',
    faqLink: 'Consulta las preguntas frecuentes.',
    intervalLabel: 'Frecuencia de facturación',
    monthly: 'Mensual',
    annual: 'Anual',
    annualBadge: '2 meses gratis',
    quantityLabel: 'Canales sociales',
    quantityHelp: 'El total de los planes de pago se actualiza de 1 a 50 canales.',
    featured: 'Más elegido',
    perMonth: '/mes',
    perYear: '/año',
    monthlyBilling: 'Facturación mensual',
    annualBilling: '{total} facturados una vez al año',
    annualEquivalent: 'Equivale a {monthly}/mes',
    freeForever: 'Gratis para siempre. Sin método de pago.',
    choosePlan: 'Elegir {plan}',
    chooseFree: 'Empezar gratis',
    member: '{count} usuario',
    members: '{count} usuarios',
    channel: '{count} canal social',
    channels: '{count} canales sociales',
    scheduledPerChannel: '{count} publicaciones programadas por canal',
    trial: 'Una prueba Team de 14 días, sin tarjeta',
    taxNotice: 'Catálogo base en EUR. Paddle calcula los impuestos de la transacción para tu ubicación y los muestra antes del consentimiento. Paddle es el Merchant of Record.',
    benchmarkTitle: 'Comparación transparente con Buffer',
    benchmarkIntro: 'Referencia D07 capturada el 24 de julio de 2026. Compara la misma cantidad de canales y período, antes de impuestos de transacción.',
    benchmarkPlan: 'Plan equivalente',
    postqron: 'Total Postqron',
    buffer: 'Buffer convertido a EUR',
    saving: 'Ahorras {amount}',
    comparisonScope: 'Pro se compara con Buffer Essentials; Team con Buffer Team. Start iguala Buffer Free hasta 3 canales.',
    comparisonLimits: 'Es una comparación comercial, no equivalencia funcional: al lanzamiento Postqron admite menos redes, 500 publicaciones programadas por canal, hasta 15 usuarios Team y no incluye analytics, bandeja, IA, API ni aprobaciones de Buffer.',
  },
  fr: {
    seoTitle: 'Tarifs et abonnements — Postqron',
    seoDescription: 'Comparez Start, Pro et Team avec une tarification progressive, des limites claires et une référence Buffer transparente.',
    eyebrow: 'Tarifs',
    heroTitle: 'Un prix clair pour chaque canal.',
    heroDescription: 'Choisissez une facturation mensuelle ou annuelle, le nombre de canaux et consultez le total récurrent complet avant le paiement.',
    loading: 'Chargement des abonnements…',
    unavailable: 'Les tarifs sont temporairement indisponibles',
    unavailableDetail: 'Nous n’affichons pas de tarifs de secours obsolètes. Réessayez pour charger le catalogue officiel du serveur.',
    retry: 'Réessayer',
    includedTitle: 'Tous les abonnements incluent',
    includedCalendar: 'Calendrier éditorial',
    includedDrafts: 'Brouillons et programmation',
    includedStatus: 'Statut des publications',
    includedPrivacy: 'Contrôles de confidentialité et sécurité',
    faqPrompt: 'Encore des questions ?',
    faqLink: 'Consultez les questions fréquentes.',
    intervalLabel: 'Fréquence de facturation',
    monthly: 'Mensuel',
    annual: 'Annuel',
    annualBadge: '2 mois offerts',
    quantityLabel: 'Canaux sociaux',
    quantityHelp: 'Le total des abonnements payants est recalculé de 1 à 50 canaux.',
    featured: 'Le plus choisi',
    perMonth: '/mois',
    perYear: '/an',
    monthlyBilling: 'Facturation mensuelle',
    annualBilling: '{total} facturés une fois par an',
    annualEquivalent: 'Soit {monthly}/mois',
    freeForever: 'Gratuit à vie. Aucun moyen de paiement.',
    choosePlan: 'Choisir {plan}',
    chooseFree: 'Commencer gratuitement',
    member: '{count} utilisateur',
    members: '{count} utilisateurs',
    channel: '{count} canal social',
    channels: '{count} canaux sociaux',
    scheduledPerChannel: '{count} publications programmées par canal',
    trial: 'Un essai Team de 14 jours, sans carte',
    taxNotice: 'Catalogue de base en EUR. Paddle calcule les taxes de transaction selon votre localisation et les affiche avant le consentement. Paddle est le Merchant of Record.',
    benchmarkTitle: 'Comparaison transparente avec Buffer',
    benchmarkIntro: 'Référence D07 relevée le 24 juillet 2026. Elle compare le même nombre de canaux et la même période, hors taxes de transaction.',
    benchmarkPlan: 'Abonnement équivalent',
    postqron: 'Total Postqron',
    buffer: 'Buffer converti en EUR',
    saving: 'Vous économisez {amount}',
    comparisonScope: 'Pro est comparé à Buffer Essentials ; Team à Buffer Team. Start égale Buffer Free jusqu’à 3 canaux.',
    comparisonLimits: 'Il s’agit d’une comparaison commerciale, pas d’une équivalence fonctionnelle : au lancement, Postqron prend en charge moins de réseaux, 500 publications par canal, jusqu’à 15 utilisateurs Team et n’inclut pas les analytics, la boîte de réception, l’IA, l’API ni les approbations de Buffer.',
  },
  de: {
    seoTitle: 'Preise und Tarife — Postqron',
    seoDescription: 'Vergleiche Start, Pro und Team mit Staffelpreisen, klaren Limits und einem transparenten Buffer-Benchmark.',
    eyebrow: 'Preise',
    heroTitle: 'Ein klarer Preis für jeden Kanal.',
    heroDescription: 'Wähle monatliche oder jährliche Abrechnung und die Kanalanzahl und sieh vor dem Checkout den vollständigen wiederkehrenden Gesamtpreis.',
    loading: 'Tarife werden geladen…',
    unavailable: 'Preise sind vorübergehend nicht verfügbar',
    unavailableDetail: 'Wir zeigen keine veralteten Ersatzpreise. Versuche erneut, den offiziellen Serverkatalog zu laden.',
    retry: 'Erneut versuchen',
    includedTitle: 'Alle Tarife enthalten',
    includedCalendar: 'Redaktionskalender',
    includedDrafts: 'Entwürfe und Planung',
    includedStatus: 'Veröffentlichungsstatus',
    includedPrivacy: 'Datenschutz- und Sicherheitskontrollen',
    faqPrompt: 'Noch unsicher?',
    faqLink: 'Lies die häufig gestellten Fragen.',
    intervalLabel: 'Abrechnungszeitraum',
    monthly: 'Monatlich',
    annual: 'Jährlich',
    annualBadge: '2 Monate gratis',
    quantityLabel: 'Social-Media-Kanäle',
    quantityHelp: 'Der Gesamtpreis der Bezahlpläne wird für 1 bis 50 Kanäle neu berechnet.',
    featured: 'Am häufigsten gewählt',
    perMonth: '/Monat',
    perYear: '/Jahr',
    monthlyBilling: 'Monatliche Abrechnung',
    annualBilling: '{total} einmal jährlich abgerechnet',
    annualEquivalent: 'Entspricht {monthly}/Monat',
    freeForever: 'Dauerhaft kostenlos. Keine Zahlungsmethode.',
    choosePlan: '{plan} wählen',
    chooseFree: 'Kostenlos starten',
    member: '{count} Benutzer',
    members: '{count} Benutzer',
    channel: '{count} Social-Media-Kanal',
    channels: '{count} Social-Media-Kanäle',
    scheduledPerChannel: '{count} geplante Beiträge pro Kanal',
    trial: 'Einmalig 14 Tage Team testen, ohne Karte',
    taxNotice: 'Basiskatalog in EUR. Paddle berechnet Transaktionssteuern für deinen Standort und zeigt sie vor der Zustimmung an. Paddle ist der Merchant of Record.',
    benchmarkTitle: 'Transparenter Buffer-Vergleich',
    benchmarkIntro: 'D07-Benchmark vom 24. Juli 2026. Verglichen werden dieselbe Kanalanzahl und derselbe Zeitraum vor Transaktionssteuern.',
    benchmarkPlan: 'Vergleichbarer Tarif',
    postqron: 'Postqron gesamt',
    buffer: 'Buffer in EUR umgerechnet',
    saving: 'Du sparst {amount}',
    comparisonScope: 'Pro wird mit Buffer Essentials verglichen, Team mit Buffer Team. Start entspricht Buffer Free bis zu 3 Kanälen.',
    comparisonLimits: 'Dies ist ein kommerzieller Vergleich, keine Funktionsgleichheit: Zum Start unterstützt Postqron weniger Netzwerke, 500 geplante Beiträge pro Kanal, bis zu 15 Team-Benutzer und enthält nicht Buffers Analytics, Inbox, KI, API oder Freigaben.',
  },
}

const PLAN_CODES = new Set<PublicPlanCode>(['start', 'pro', 'team'])
const BUFFER_EUR_USD_RATE = 1.1377

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function isPositiveInteger(value: unknown): value is number {
  return Number.isInteger(value) && Number(value) >= 0
}

function isMoney(value: unknown): value is Money {
  return isRecord(value)
    && isPositiveInteger(value.amount_cents)
    && value.currency === 'EUR'
}

function isTier(value: unknown): value is PriceTier {
  return isRecord(value)
    && Number.isInteger(value.from_channel)
    && Number.isInteger(value.to_channel)
    && Number(value.from_channel) >= 1
    && Number(value.to_channel) >= Number(value.from_channel)
    && isMoney(value.monthly)
    && isMoney(value.annual)
}

function isTrial(value: unknown): value is NonNullable<PublicPlan['trial']> {
  return isRecord(value)
    && isPositiveInteger(value.days)
    && isPositiveInteger(value.members)
    && isPositiveInteger(value.channels)
    && isPositiveInteger(value.scheduled_publications_per_channel)
}

function isPlan(value: unknown): value is PublicPlan {
  if (!isRecord(value) || !isRecord(value.prices) || !isRecord(value.limits)) {
    return false
  }
  return typeof value.code === 'string'
    && PLAN_CODES.has(value.code as PublicPlanCode)
    && typeof value.name === 'string'
    && value.name.length > 0
    && typeof value.purchasable === 'boolean'
    && isMoney(value.prices.monthly)
    && isMoney(value.prices.annual)
    && Array.isArray(value.price_tiers)
    && value.price_tiers.every(isTier)
    && isPositiveInteger(value.limits.members)
    && isPositiveInteger(value.limits.channels)
    && isPositiveInteger(value.limits.scheduled_publications)
    && isPositiveInteger(value.limits.scheduled_publications_per_channel)
    && (value.trial === undefined || isTrial(value.trial))
}

function validatePlanShape(plan: PublicPlan): boolean {
  if (plan.code === 'start') {
    return !plan.purchasable
      && plan.price_tiers.length === 0
      && plan.prices.monthly.amount_cents === 0
      && plan.prices.annual.amount_cents === 0
  }
  if (!plan.purchasable || plan.price_tiers.length !== 3) {
    return false
  }
  const expectedBounds = [[1, 10], [11, 25], [26, 50]]
  return plan.price_tiers.every((tier, index) =>
    tier.from_channel === expectedBounds[index]?.[0]
    && tier.to_channel === expectedBounds[index]?.[1]
    && tier.monthly.currency === plan.prices.monthly.currency
    && tier.annual.currency === plan.prices.annual.currency)
}

export function parsePublicPlan(value: unknown): PublicPlan {
  const normalized = isRecord(value) && value.price_tiers === undefined
    ? { ...value, price_tiers: [] }
    : value
  if (!isPlan(normalized) || !validatePlanShape(normalized)) {
    throw new Error('PUBLIC_PLAN_INVALID')
  }
  return normalized
}

export function parsePublicCatalog(value: unknown): PublicCatalog {
  if (!isRecord(value)) {
    throw new Error('PUBLIC_CATALOG_UNAVAILABLE')
  }
  if (
    value.provider !== 'paddle'
    || value.catalog_version !== 'd07-v1'
    || value.currency !== 'EUR'
    || !Array.isArray(value.plans)
    || value.plans.length !== 3
  ) {
    throw new Error('PUBLIC_CATALOG_INVALID')
  }
  let plans: PublicPlan[]
  try {
    plans = value.plans.map(parsePublicPlan)
  } catch {
    throw new Error('PUBLIC_CATALOG_INVALID')
  }
  if (new Set(plans.map(plan => plan.code)).size !== 3) {
    throw new Error('PUBLIC_CATALOG_DUPLICATED')
  }
  return { ...value, plans } as unknown as PublicCatalog
}

export function localeFromPath(path: string): PricingLocale {
  const candidate = path.split(/[/?#]/u).filter(Boolean)[0]
  return PRICING_LOCALES.includes(candidate as PricingLocale)
    ? candidate as PricingLocale
    : 'en'
}

export function localizePath(locale: PricingLocale, path: string): string {
  const normalized = path.startsWith('/') ? path : `/${path}`
  const parts = normalized.split('/')
  if (PRICING_LOCALES.includes(parts[1] as PricingLocale)) {
    parts.splice(1, 1)
  }
  const base = parts.join('/') || '/'
  return `/${locale}${base === '/' ? '' : base}`
}

export function pricingCopy(locale: PricingLocale): PricingCopy {
  return PRICING_COPY[locale] ?? PRICING_COPY.en
}

export function interpolate(
  message: string,
  parameters: Readonly<Record<string, string | number>>,
): string {
  return message.replace(/\{([A-Za-z][A-Za-z0-9_]*)\}/gu, (_match, key: string) =>
    String(parameters[key] ?? `{${key}}`))
}

export function formatMoney(
  money: Money,
  locale: PricingLocale = 'en',
): string {
  return new Intl.NumberFormat(locale, {
    style: 'currency',
    currency: money.currency,
    minimumFractionDigits: money.amount_cents % 100 === 0 ? 0 : 2,
  }).format(money.amount_cents / 100)
}

function tierQuantity(channels: number, tier: PriceTier): number {
  if (channels < tier.from_channel) {
    return 0
  }
  return Math.min(channels, tier.to_channel) - tier.from_channel + 1
}

export function priceForChannels(
  plan: PublicPlan,
  interval: BillingInterval,
  channels: number,
): Money {
  if (
    !Number.isSafeInteger(channels)
    || channels < 1
    || channels > plan.limits.channels
  ) {
    throw new Error('PUBLIC_CATALOG_INVALID_QUANTITY')
  }
  if (!plan.purchasable) {
    return { amount_cents: 0, currency: 'EUR' }
  }
  return {
    amount_cents: plan.price_tiers.reduce((total, tier) =>
      total + tierQuantity(channels, tier) * tier[interval].amount_cents, 0),
    currency: plan.prices[interval].currency,
  }
}

export function monthlyEquivalent(total: Money, interval: BillingInterval): Money {
  return interval === 'monthly'
    ? total
    : {
        amount_cents: Math.round(total.amount_cents / 12),
        currency: total.currency,
      }
}

function bufferMonthlyUSD(plan: 'pro' | 'team', channels: number): number {
  if (!Number.isSafeInteger(channels) || channels < 1 || channels > 50) {
    throw new Error('BUFFER_BENCHMARK_INVALID_QUANTITY')
  }
  const firstTier = plan === 'pro' ? 6 : 12
  if (channels <= 10) {
    return firstTier * channels
  }
  const firstTen = firstTier * 10
  if (channels <= 25) {
    return firstTen + 4 * (channels - 10)
  }
  return firstTen + 60 + 3 * (channels - 25)
}

export function bufferBenchmark(
  plan: 'pro' | 'team',
  interval: BillingInterval,
  channels: number,
): Money {
  const usd = bufferMonthlyUSD(plan, channels)
    * (interval === 'annual' ? 10 : 1)
  return {
    amount_cents: Math.round((usd / BUFFER_EUR_USD_RATE) * 100),
    currency: 'EUR',
  }
}

export function savingsAgainstBuffer(
  plan: PublicPlan,
  interval: BillingInterval,
  channels: number,
): Money {
  if (plan.code !== 'pro' && plan.code !== 'team') {
    return { amount_cents: 0, currency: 'EUR' }
  }
  const postqron = priceForChannels(plan, interval, channels)
  const buffer = bufferBenchmark(plan.code, interval, channels)
  return {
    amount_cents: Math.max(0, buffer.amount_cents - postqron.amount_cents),
    currency: 'EUR',
  }
}

export function purchaseHref(
  appUrl: string,
  locale: PricingLocale,
  plan: PublicPlan,
  interval: BillingInterval,
  channels: number,
): string {
  const localizedApp = /^https?:\/\//u.test(appUrl)
    ? appUrl
    : localizePath(locale, appUrl)
  const url = new URL(localizedApp, 'https://postqron.local')
  url.searchParams.set('plan', plan.code)
  url.searchParams.set('interval', interval)
  url.searchParams.set(
    'quantity',
    String(plan.purchasable ? channels : plan.limits.channels),
  )
  return /^https?:\/\//u.test(appUrl)
    ? url.href
    : `${url.pathname}${url.search}`
}
