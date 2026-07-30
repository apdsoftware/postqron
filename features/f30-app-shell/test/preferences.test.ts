import assert from 'node:assert/strict'
import test from 'node:test'
import {
  formatDateTime,
  localeOptions,
  timezoneGroups,
  timezoneValues,
} from '../components/core/preferences.ts'

test('locale options expose every supported language and are stable', () => {
  const options = localeOptions('it-IT')
  const values = options.map(option => option.value)
  for (const expected of ['en', 'it', 'it-IT', 'es', 'fr', 'de']) {
    assert.ok(values.includes(expected), `missing ${expected}`)
  }
  assert.ok(options.every(option => option.label.trim() !== ''))
  // A supported value is not duplicated at the front of the list.
  assert.equal(values.filter(value => value === 'it-IT').length, 1)
})

test('locale options preserve an unknown stored value so it stays selectable', () => {
  const options = localeOptions('pt-BR')
  assert.equal(options[0]?.value, 'pt-BR')
  assert.equal(options[0]?.label, 'pt-BR')
  assert.ok(options.some(option => option.value === 'it-IT'))
})

test('locale options ignore an empty stored value', () => {
  const options = localeOptions('')
  assert.ok(options.every(option => option.value !== ''))
  assert.equal(options[0]?.value, 'en')
})

test('timezone values return a non-empty catalog', () => {
  const zones = timezoneValues()
  assert.ok(zones.length > 0)
  assert.ok(zones.includes('Europe/Rome') || zones.includes('UTC'))
})

test('timezone groups are sorted, region-keyed and include the current zone', () => {
  const groups = timezoneGroups('Europe/Rome')
  const europe = groups.find(group => group.region === 'Europe')
  assert.ok(europe, 'Europe group present')
  assert.ok(europe?.zones.includes('Europe/Rome'))
  const regions = groups.map(group => group.region)
  assert.deepEqual(regions, [...regions].sort((a, b) => a.localeCompare(b)))
  for (const group of groups) {
    assert.deepEqual(
      group.zones,
      [...group.zones].sort((a, b) => a.localeCompare(b)),
    )
  }
})

test('timezone groups surface a stored zone outside the standard catalog', () => {
  const groups = timezoneGroups('Mars/Olympus')
  const region = groups.find(group => group.region === 'Mars')
  assert.ok(region, 'custom region present')
  assert.ok(region?.zones.includes('Mars/Olympus'))
})

test('date formatting localises a valid instant and stays deterministic', () => {
  const iso = '2026-07-30T10:48:07.047533Z'
  const italian = formatDateTime(iso, 'it')
  const english = formatDateTime(iso, 'en')
  assert.notEqual(italian.trim(), '')
  assert.notEqual(english.trim(), '')
  assert.doesNotMatch(italian, /Invalid/u)
})

test('date formatting falls back to the raw value for an invalid input', () => {
  assert.equal(formatDateTime('not-a-date', 'it'), 'not-a-date')
})
