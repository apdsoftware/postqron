import type {
  ContentCapability,
  ThreadItem,
} from './editorial-contracts.ts'

export interface ThreadConstraintSummary {
  allowed: boolean
  compatible: boolean
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
  if (capabilities.length === 0) {
    return undefined
  }
  const allowed = capabilities.every(capability => capability.thread.allowed)
  if (!allowed) {
    return {
      allowed: false,
      compatible: true,
      required: false,
      minimumItems: 0,
      maximumItems: 0,
      maxItemCharacters: 0,
      maxMediaPerItem: 0,
    }
  }
  const relevant = capabilities
  const minimumItems = Math.max(
    0,
    ...relevant.map(capability =>
      capability.thread.minimum_items
      ?? (capability.thread.required ? 1 : 0)),
  )
  const maximumItems = smallestDefined(
    relevant.map(capability => capability.thread.maximum_items),
  )
  return {
    allowed: true,
    compatible: maximumItems === undefined || minimumItems <= maximumItems,
    required: relevant.some(capability => capability.thread.required),
    minimumItems,
    maximumItems,
    maxItemCharacters: smallestDefined(
      relevant.map(capability => capability.thread.max_item_characters),
    ),
    maxMediaPerItem: smallestDefined(
      relevant.map(capability => capability.thread.max_media_per_item),
    ),
  }
}

export function canRemoveThreadItem(
  constraints: ThreadConstraintSummary | undefined,
  itemCount: number,
): boolean {
  const minimum = constraints?.allowed && constraints.compatible
    ? constraints.minimumItems
    : 0
  return itemCount > minimum
}

export function threadItemsForSubmission(
  items: readonly ThreadItem[],
  constraints: ThreadConstraintSummary | undefined,
): ThreadItem[] {
  if (!constraints?.allowed || !constraints.compatible) {
    return []
  }
  return items.map(item => ({
    text: item.text,
    media_ids: [...item.media_ids],
  }))
}
