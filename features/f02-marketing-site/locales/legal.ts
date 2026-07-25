import { defineCatalogs, type CatalogShape } from '../../f36-i18n/src/catalog.ts'

const en = {
  'seo.titleTemplate': '{title} — Postqron',
  'hero.eyebrow': 'Legal documents',
  'doc.termini.title': 'Terms and conditions',
  'doc.termini.description': 'The terms that govern the use of Postqron.',
  'doc.privacy.title': 'Privacy Policy',
  'doc.privacy.description': 'How Postqron handles and protects personal data.',
  'doc.cookie.title': 'Cookie Policy',
  'doc.cookie.description': 'Necessary cookies, preferences, and optional tools.',
  'version.label': 'Version {version} · In force since {date}',
  'state.loading': 'Loading the document…',
  'state.unavailableTitle': 'Approved content unavailable',
  'state.unavailableBody': 'The approved content is not available. Postqron does not publish drafts or text lacking the required approval.',
  'state.retry': 'Retry',
} as const

const it: CatalogShape<typeof en> = {
  'seo.titleTemplate': '{title} — Postqron',
  'hero.eyebrow': 'Documenti legali',
  'doc.termini.title': 'Termini e condizioni',
  'doc.termini.description': 'Le condizioni che regolano l’uso di Postqron.',
  'doc.privacy.title': 'Privacy Policy',
  'doc.privacy.description': 'Come Postqron tratta e protegge i dati personali.',
  'doc.cookie.title': 'Cookie Policy',
  'doc.cookie.description': 'Cookie necessari, preferenze e strumenti opzionali.',
  'version.label': 'Versione {version} · In vigore dal {date}',
  'state.loading': 'Caricamento del documento…',
  'state.unavailableTitle': 'Contenuto approvato non disponibile',
  'state.unavailableBody': 'Il contenuto approvato non è disponibile. Postqron non pubblica bozze o testi privi dell’approvazione prevista.',
  'state.retry': 'Riprova',
}

const es: CatalogShape<typeof en> = {
  'seo.titleTemplate': '{title} — Postqron',
  'hero.eyebrow': 'Documentos legales',
  'doc.termini.title': 'Términos y condiciones',
  'doc.termini.description': 'Las condiciones que regulan el uso de Postqron.',
  'doc.privacy.title': 'Política de privacidad',
  'doc.privacy.description': 'Cómo Postqron trata y protege los datos personales.',
  'doc.cookie.title': 'Política de cookies',
  'doc.cookie.description': 'Cookies necesarias, preferencias y herramientas opcionales.',
  'version.label': 'Versión {version} · En vigor desde el {date}',
  'state.loading': 'Cargando el documento…',
  'state.unavailableTitle': 'Contenido aprobado no disponible',
  'state.unavailableBody': 'El contenido aprobado no está disponible. Postqron no publica borradores ni textos sin la aprobación requerida.',
  'state.retry': 'Reintentar',
}

const fr: CatalogShape<typeof en> = {
  'seo.titleTemplate': '{title} — Postqron',
  'hero.eyebrow': 'Documents juridiques',
  'doc.termini.title': 'Conditions générales',
  'doc.termini.description': 'Les conditions qui régissent l’utilisation de Postqron.',
  'doc.privacy.title': 'Politique de confidentialité',
  'doc.privacy.description': 'Comment Postqron traite et protège les données personnelles.',
  'doc.cookie.title': 'Politique relative aux cookies',
  'doc.cookie.description': 'Cookies nécessaires, préférences et outils facultatifs.',
  'version.label': 'Version {version} · En vigueur depuis le {date}',
  'state.loading': 'Chargement du document…',
  'state.unavailableTitle': 'Contenu approuvé indisponible',
  'state.unavailableBody': 'Le contenu approuvé n’est pas disponible. Postqron ne publie pas de brouillons ni de textes sans l’approbation requise.',
  'state.retry': 'Réessayer',
}

const de: CatalogShape<typeof en> = {
  'seo.titleTemplate': '{title} — Postqron',
  'hero.eyebrow': 'Rechtliche Dokumente',
  'doc.termini.title': 'Allgemeine Geschäftsbedingungen',
  'doc.termini.description': 'Die Bedingungen für die Nutzung von Postqron.',
  'doc.privacy.title': 'Datenschutzerklärung',
  'doc.privacy.description': 'Wie Postqron personenbezogene Daten verarbeitet und schützt.',
  'doc.cookie.title': 'Cookie-Richtlinie',
  'doc.cookie.description': 'Notwendige Cookies, Präferenzen und optionale Tools.',
  'version.label': 'Version {version} · In Kraft seit {date}',
  'state.loading': 'Dokument wird geladen…',
  'state.unavailableTitle': 'Genehmigter Inhalt nicht verfügbar',
  'state.unavailableBody': 'Der genehmigte Inhalt ist nicht verfügbar. Postqron veröffentlicht keine Entwürfe oder Texte ohne die erforderliche Genehmigung.',
  'state.retry': 'Erneut versuchen',
}

export const MARKETING_LEGAL_CATALOGS = defineCatalogs({ en, it, es, fr, de })

export type MarketingLegalMessageKey = keyof typeof en
