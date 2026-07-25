export type PrelaunchModeSource =
  | 'explicit_false'
  | 'explicit_true'
  | 'fail_closed'
  | 'non_production_default'

export interface PrelaunchMode {
  readonly enabled: boolean
  readonly source: PrelaunchModeSource
}

export function resolvePrelaunchMode(
  rawValue: string | undefined,
  nodeEnvironment: string | undefined,
): PrelaunchMode {
  if (rawValue === 'true') {
    return Object.freeze({ enabled: true, source: 'explicit_true' })
  }
  if (rawValue === 'false') {
    return Object.freeze({ enabled: false, source: 'explicit_false' })
  }
  if (nodeEnvironment === 'production') {
    return Object.freeze({ enabled: true, source: 'fail_closed' })
  }
  return Object.freeze({
    enabled: false,
    source: 'non_production_default',
  })
}

export const PRELAUNCH_MODE_STATE_KEY = 'postqron.prelaunch.mode'
