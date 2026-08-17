/**
 * Conteggio animato dei numeri della fascia statistiche.
 *
 * Sostituisce jquery.counterUp: due funzioni pure al posto di un plugin, con il
 * vantaggio che si possono provare senza un DOM.
 */

/**
 * Valore da mostrare a una certa frazione dell'animazione.
 *
 * La curva è un ease-out cubico: parte veloce e si posa sul valore finale,
 * invece di arrivarci di colpo come farebbe un conteggio lineare. Il progresso
 * viene limitato a [0, 1], così un frame in ritardo non fa superare il totale.
 */
export function countUpValue(target: number, progress: number): number {
  const clamped = Math.min(Math.max(progress, 0), 1)
  const eased = 1 - (1 - clamped) ** 3
  return Math.round(target * eased)
}

/**
 * Formatta un intero con il punto come separatore delle migliaia.
 *
 * Non usa `toLocaleString`: il numero viene reso una prima volta sul server in
 * fase di pre-rendering e una seconda nel browser, e due implementazioni ICU
 * diverse produrrebbero un errore di idratazione su una stringa qualsiasi.
 */
export function formatCount(value: number): string {
  const sign = value < 0 ? '-' : ''
  const digits = Math.abs(Math.trunc(value)).toString()
  const groups: string[] = []

  for (let end = digits.length; end > 0; end -= 3) {
    groups.unshift(digits.slice(Math.max(end - 3, 0), end))
  }

  return sign + groups.join('.')
}
