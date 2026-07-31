import type { ContentCapability } from './editorial-contracts.ts'

export interface ThreadConstraintSummary {
  maxItemCharacters?: number
  maxMediaPerItem?: number
  maximumItems?: number
  minimumItems: number
  required: boolean
}

function smallestDefined(values: ReadonlyArray<number | undefined>): number | undefined {
  const defined = values.filter((value): value is number => typeof value === 'number')
  return defined.length > 0 ? Math.min(...defined) : undefined
}

export function aggregateThreadConstraints(
  capabilities: readonly ContentCapability[],
): ThreadConstraintSummary | undefined {
  const relevant = capabilities.filter(capability => capability.thread.allowed)
  if (relevant.length === 0) {
    return undefined
  }
  return {
    required: relevant.some(capability => capability.thread.required),
    minimumItems: Math.max(
      0,
      ...relevant.map(capability =>
        capability.thread.minimum_items
        ?? (capability.thread.required ? 1 : 0)),
    ),
    maximumItems: smallestDefined(
      relevant.map(capability => capability.thread.maximum_items),
    ),
    maxItemCharacters: smallestDefined(
      relevant.map(capability => capability.thread.max_item_characters),
    ),
    maxMediaPerItem: smallestDefined(
      relevant.map(capability => capability.thread.max_media_per_item),
    ),
  }
}
