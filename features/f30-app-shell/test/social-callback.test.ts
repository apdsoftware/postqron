import assert from 'node:assert/strict'
import test from 'node:test'
import { SocialApiError } from '../components/core/social-api.ts'
import {
  parseSocialCallbackDocument,
  socialCallbackHandoffDocument,
  socialOAuthCallbackInput,
  withoutSocialOAuthCallbackParameters,
} from '../components/core/social-callback.ts'

test('callback URL cleanup removes only OAuth response parameters', () => {
  assert.deepEqual(withoutSocialOAuthCallbackParameters({
    state: 'secret-state',
    code: 'secret-code',
    iss: 'https://issuer.example.test',
    error: 'access_denied',
    campaign: 'preserved',
    filter: ['one', 'two'],
  }), {
    campaign: 'preserved',
    filter: ['one', 'two'],
  })
})

test('callback handoff parses the JSON selection document returned by F5', () => {
  const selection = parseSocialCallbackDocument(JSON.stringify({
    selection_id: 'sel-1',
    provider: 'facebook_pages',
    expires_at: '2026-07-30T10:10:00.000Z',
    resources: [{
      remote_id: 'page-1',
      resource_type: 'facebook_page',
      account_type: 'page',
      display_name: 'Studio Page',
      scopes: ['pages_manage_posts'],
    }],
  }))

  assert.equal(selection.selection_id, 'sel-1')
  assert.equal(selection.resources[0]?.display_name, 'Studio Page')
})

test('callback handoff fails closed when the document is empty or invalid JSON', () => {
  assert.throws(
    () => parseSocialCallbackDocument(''),
    (error: unknown) =>
      error instanceof SocialApiError
      && error.code === 'social_invalid_callback_document'
      && error.kind === 'unavailable',
  )

  assert.throws(
    () => parseSocialCallbackDocument('{invalid json'),
    (error: unknown) =>
      error instanceof SocialApiError
      && error.code === 'social_invalid_callback_document'
      && error.kind === 'unavailable',
  )
})

test('callback handoff maps flat F5 errors back to stable UI failure kinds', () => {
  assert.throws(
    () => parseSocialCallbackDocument(JSON.stringify({
      code: 'provider_denied',
      message: 'Denied by provider',
      retryable: false,
    })),
    (error: unknown) =>
      error instanceof SocialApiError
      && error.code === 'provider_denied'
      && error.kind === 'provider-denied',
  )
})

test('same-origin relay forwards only one state/result/issuer tuple', () => {
  assert.deepEqual(socialOAuthCallbackInput({
    state: 'opaque-state',
    code: 'authorization-code',
    iss: 'https://issuer.example.test',
    redirect: 'https://evil.example.test',
  }), {
    state: 'opaque-state',
    code: 'authorization-code',
    error: '',
    iss: 'https://issuer.example.test',
  })
  assert.throws(
    () => socialOAuthCallbackInput({ state: ['one', 'two'], code: 'code' }),
    (error: unknown) => error instanceof SocialApiError
      && error.code === 'social_invalid_callback_parameters',
  )
  assert.throws(
    () => socialOAuthCallbackInput({ state: 'state', code: 'code', error: 'denied' }),
    (error: unknown) => error instanceof SocialApiError
      && error.code === 'social_invalid_callback_parameters',
  )
})

test('relay handoff serializes only the client-safe F5 error envelope', () => {
  const error = new SocialApiError({
    code: 'invalid_oauth_state',
    kind: 'invalid-state',
    message: 'Conflict',
    retryable: false,
    status: 409,
  })
  assert.deepEqual(JSON.parse(socialCallbackHandoffDocument(error)), {
    code: 'invalid_oauth_state',
    message: 'Conflict',
    retryable: false,
  })
})
