import type { NavItem } from '~/types/content'

/**
 * Menu del sito pubblico.
 *
 * Per ora punta solo ad ancore della home: sono le uniche destinazioni che
 * esistono. Il pre-rendering ha `failOnError` attivo e segue i link, quindi una
 * voce verso una pagina non ancora scritta farebbe fallire la build — non è una
 * dimenticanza, è il motivo per cui le pagine di #402–#404 aggiungeranno qui la
 * propria voce insieme alla propria rotta.
 */
export const mainNav: readonly NavItem[] = [
  { label: 'Home', to: '/#welcome' },
  {
    label: 'Prodotto',
    children: [
      { label: 'Funzionalità', to: '/#funzionalita' },
      { label: 'Testimonianze', to: '/#testimonianze' },
      { label: 'Prezzi', to: '/#prezzi' },
    ],
  },
  {
    label: 'Risorse',
    children: [
      { label: 'API e webhook', to: '/#api' },
      { label: 'Dal blog', to: '/#blog' },
    ],
  },
  { label: 'Contatti', to: '/#contatti' },
]

/** Etichetta e destinazione del pulsante in fondo al menu. */
export const navCta = { label: 'Prova gratis', to: '/#welcome' } as const

export const footerNav: readonly { title: string, items: readonly NavItem[] }[] = [
  {
    title: 'Prodotto',
    items: [
      { label: 'Funzionalità', to: '/#funzionalita' },
      { label: 'Prezzi', to: '/#prezzi' },
      { label: 'API e webhook', to: '/#api' },
      { label: 'Blog', to: '/#blog' },
    ],
  },
  {
    title: 'Assistenza',
    items: [
      { label: 'Stato del servizio', to: '/#statistiche' },
      { label: 'Testimonianze', to: '/#testimonianze' },
      { label: 'Contatti', to: '/#contatti' },
    ],
  },
]
