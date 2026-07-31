import type {
  ComposerDestination,
  ContentCapability,
} from './editorial-contracts.ts'

export function applyDestinationCapability(
  destination: ComposerDestination,
  capability: ContentCapability,
): void {
  destination.capability_id = capability.id
  destination.format = capability.format
  const previous = destination.fields ?? {}
  destination.fields = Object.fromEntries(
    (capability.fields ?? []).map(field => [field.name, previous[field.name] ?? '']),
  )
}

export function setDestinationField(
  destination: ComposerDestination,
  name: string,
  value: string,
): void {
  destination.fields = {
    ...(destination.fields ?? {}),
    [name]: value,
  }
}
