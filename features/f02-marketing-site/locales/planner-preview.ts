import { defineCatalogs, type CatalogShape } from '../../f36-i18n/src/catalog.ts'

const en = {
  'aria.previewLabel': 'Preview of the Postqron editorial calendar',
  'month.label': 'July 2026',
  'day.mon': 'Mon',
  'day.tue': 'Tue',
  'day.wed': 'Wed',
  'day.thu': 'Thu',
  'day.fri': 'Fri',
  'card1.channels': 'Instagram · LinkedIn',
  'card1.title': 'Behind the scenes of the new project',
  'card2.channels': 'Facebook',
  'card2.title': 'Three tips to work better',
  'card.todayAt': 'Today, {time}',
} as const

const it: CatalogShape<typeof en> = {
  'aria.previewLabel': 'Anteprima del calendario editoriale Postqron',
  'month.label': 'Luglio 2026',
  'day.mon': 'Lun',
  'day.tue': 'Mar',
  'day.wed': 'Mer',
  'day.thu': 'Gio',
  'day.fri': 'Ven',
  'card1.channels': 'Instagram · LinkedIn',
  'card1.title': 'Dietro le quinte del nuovo progetto',
  'card2.channels': 'Facebook',
  'card2.title': 'Tre consigli per lavorare meglio',
  'card.todayAt': 'Oggi, {time}',
}

const es: CatalogShape<typeof en> = {
  'aria.previewLabel': 'Vista previa del calendario editorial de Postqron',
  'month.label': 'Julio de 2026',
  'day.mon': 'Lun',
  'day.tue': 'Mar',
  'day.wed': 'Mié',
  'day.thu': 'Jue',
  'day.fri': 'Vie',
  'card1.channels': 'Instagram · LinkedIn',
  'card1.title': 'Detrás de escena del nuevo proyecto',
  'card2.channels': 'Facebook',
  'card2.title': 'Tres consejos para trabajar mejor',
  'card.todayAt': 'Hoy, {time}',
}

const fr: CatalogShape<typeof en> = {
  'aria.previewLabel': 'Aperçu du calendrier éditorial Postqron',
  'month.label': 'Juillet 2026',
  'day.mon': 'Lun',
  'day.tue': 'Mar',
  'day.wed': 'Mer',
  'day.thu': 'Jeu',
  'day.fri': 'Ven',
  'card1.channels': 'Instagram · LinkedIn',
  'card1.title': 'Dans les coulisses du nouveau projet',
  'card2.channels': 'Facebook',
  'card2.title': 'Trois conseils pour mieux travailler',
  'card.todayAt': 'Aujourd’hui, {time}',
}

const de: CatalogShape<typeof en> = {
  'aria.previewLabel': 'Vorschau des Postqron-Redaktionskalenders',
  'month.label': 'Juli 2026',
  'day.mon': 'Mo',
  'day.tue': 'Di',
  'day.wed': 'Mi',
  'day.thu': 'Do',
  'day.fri': 'Fr',
  'card1.channels': 'Instagram · LinkedIn',
  'card1.title': 'Hinter den Kulissen des neuen Projekts',
  'card2.channels': 'Facebook',
  'card2.title': 'Drei Tipps für besseres Arbeiten',
  'card.todayAt': 'Heute, {time}',
}

export const MARKETING_PLANNER_PREVIEW_CATALOGS = defineCatalogs({ en, it, es, fr, de })

export type MarketingPlannerPreviewMessageKey = keyof typeof en
