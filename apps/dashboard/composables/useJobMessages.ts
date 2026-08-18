import type { JobField, JobIssue } from '~/utils/jobs'
import { JOB_LIMITS } from '~/utils/job-contract'
import { issueFor } from '~/utils/jobs'
import { fill } from '~/utils/text'

/**
 * Traduce un motivo di rifiuto nella frase che l'utente legge.
 *
 * ## Perché non lo fa `utils/jobs.ts`
 *
 * Perché lì i testi non ci sono, e non devono esserci: `validateDraft` è logica
 * pura, verificabile senza montare niente e senza sapere che le lingue sono
 * cinque. Produce **chiavi**; la frase la sceglie qui chi ha accesso a
 * `useLocale()`.
 *
 * ## I numeri dentro le frasi
 *
 * Vengono da due posti diversi e nessuno dei due è questo file. Il limite che
 * un rifiuto nomina — «al massimo {limit} caratteri» — arriva dentro il
 * [JobIssue], cioè da `utils/job-contract.ts`, che è la copia allineata al
 * backend. I confini del timeout arrivano direttamente da lì. Una frase che
 * scrivesse un numero per conto proprio prometterebbe un limite che il server
 * non ha, in cinque lingue.
 */
export function useJobMessages(): {
  messageFor: (issue: JobIssue | null | undefined) => string
  messageForField: (issues: readonly JobIssue[], field: JobField) => string
} {
  const { t } = useLocale()

  function messageFor(issue: JobIssue | null | undefined): string {
    if (!issue) return ''

    const values: Record<string, string | number> = {
      // I confini del timeout sono gli unici che una frase nomina in coppia, e
      // sono gli stessi che `Job.validateExecution` applica.
      min: JOB_LIMITS.minTimeoutSeconds,
      max: JOB_LIMITS.maxTimeoutSeconds,
    }
    if (issue.limit !== undefined) values.limit = issue.limit
    if (issue.value !== undefined) values.value = issue.value

    return fill(t.value.jobs.errors[issue.code], values)
  }

  return {
    messageFor,
    messageForField: (issues, field) => messageFor(issueFor(issues, field)),
  }
}
