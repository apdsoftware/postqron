import type { AppShellMessageKey } from './catalogs.ts'
import type { ValidationError } from './editorial-contracts.ts'

type Translate = (
  key: AppShellMessageKey,
  parameters?: Readonly<Record<string, string | number>>,
) => string

const codeKeys = new Set([
  'channel_id_required',
  'destination_id_duplicate',
  'destination_id_required',
  'destinations_required',
  'media_id_duplicate',
  'media_id_required',
  'media_storage_key_required',
  'text_required',
  'text_too_long',
  'video_fast_start_required',
])

const ruleKeys = new Set([
  'max_item_characters',
  'max_media_per_item',
  'maximum_code_points',
  'maximum_items',
  'minimum_items',
  'required',
  'unique',
])

export function localizedValidationField(
  field: string,
  t: Translate,
): string {
  if (field === 'destinations') {
    return t('composer.field.destinations')
  }
  if (field.startsWith('text')) {
    return t('composer.field.text')
  }
  if (field.startsWith('link')) {
    return t('composer.field.link')
  }
  if (field.startsWith('media[')) {
    return t('composer.field.media')
  }
  if (field.startsWith('thread[')) {
    return t('composer.field.thread')
  }
  return field
}

export function localizedValidationMessage(
  error: ValidationError,
  t: Translate,
): string {
  if (codeKeys.has(error.code)) {
    return t(`composer.validation.code.${error.code}` as AppShellMessageKey)
  }
  if (ruleKeys.has(error.rule)) {
    return t(`composer.validation.rule.${error.rule}` as AppShellMessageKey)
  }
  return error.message
}
