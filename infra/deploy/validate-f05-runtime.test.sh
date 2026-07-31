#!/usr/bin/env bash

set -euo pipefail

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
validator="$script_dir/validate-f05-runtime.sh"
composer="$script_dir/compose-f05-runtime.sh"
compose="$script_dir/compose.yaml"
workflow="$script_dir/../../.github/workflows/deploy.yml"
temporary_dir=$(mktemp -d)
trap 'rm -rf "$temporary_dir"' EXIT

cipher_key=$(printf '0123456789abcdef0123456789abcdef' | openssl base64 -A)
callback=https://postqron.com/app/social-oauth/callback
if canary_expires_at=$(date -u -d '+1 hour' '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null); then
  canary_too_long_at=$(date -u -d '+3 hours' '+%Y-%m-%dT%H:%M:%SZ')
  :
else
  canary_expires_at=$(date -j -u -v+1H '+%Y-%m-%dT%H:%M:%SZ')
  canary_too_long_at=$(date -j -u -v+3H '+%Y-%m-%dT%H:%M:%SZ')
fi

write_base() {
  local target=$1
  printf '%s\n' \
    'POSTQRON_F05_ENABLED=true' \
    'POSTQRON_F05_CIPHER_KEY_ID=fixture-key' \
    "POSTQRON_F05_CIPHER_KEY_BASE64=$cipher_key" \
    > "$target"
}

compose_x_runtime() {
  local target=$1
  local dedicated_cipher=$2
  local smoke_verified=$3
  local canary_enabled=$4
  local canary_workspace=$5
  local canary_actor=$6
  local canary_expiry=$7
  env \
    POSTQRON_F05_ENABLED=true \
    POSTQRON_F05_CIPHER_KEY_ID=dedicated-fixture-key \
    POSTQRON_F05_CIPHER_KEY_BASE64="$dedicated_cipher" \
    POSTQRON_F05_X_ENABLED=true \
    POSTQRON_F05_X_CLIENT_ID=dedicated-fixture-client \
    POSTQRON_F05_X_CLIENT_SECRET=NEVER_PRINT_DEDICATED_SECRET \
    POSTQRON_F05_X_REDIRECT_URL="$callback" \
    POSTQRON_F05_X_API_ACCESS_APPROVED=true \
    POSTQRON_F05_X_RUNTIME_AUDIT_VERIFIED=true \
    POSTQRON_F05_X_SMOKE_TEST_VERIFIED="$smoke_verified" \
    POSTQRON_F05_X_FIRST_SMOKE_CANARY_ENABLED="$canary_enabled" \
    POSTQRON_F05_X_FIRST_SMOKE_CANARY_WORKSPACE_ID="$canary_workspace" \
    POSTQRON_F05_X_FIRST_SMOKE_CANARY_ACTOR_ACCOUNT_ID="$canary_actor" \
    POSTQRON_F05_X_FIRST_SMOKE_CANARY_EXPIRES_AT="$canary_expiry" \
    "$composer" "$target" postqron.com production
}

append_ready_provider() {
  local provider=$1
  local target=$2
  case "$provider" in
    facebook_pages)
      printf '%s\n' \
        'POSTQRON_F05_META_ENABLED=true' \
        'POSTQRON_F05_META_GRAPH_VERSION=v25.0' \
        'POSTQRON_F05_FACEBOOK_CLIENT_ID=fixture-client' \
        'POSTQRON_F05_FACEBOOK_CLIENT_SECRET=fixture-secret' \
        "POSTQRON_F05_FACEBOOK_REDIRECT_URL=$callback" \
        'POSTQRON_F05_FACEBOOK_LOGIN_CONFIG_ID=fixture-config' \
        'POSTQRON_F05_FACEBOOK_APP_REVIEW_APPROVED=true' \
        'POSTQRON_F05_FACEBOOK_RUNTIME_AUDIT_VERIFIED=true' \
        >> "$target"
      ;;
    instagram_professional)
      printf '%s\n' \
        'POSTQRON_F05_META_ENABLED=true' \
        'POSTQRON_F05_META_GRAPH_VERSION=v25.0' \
        'POSTQRON_F05_INSTAGRAM_CLIENT_ID=fixture-client' \
        'POSTQRON_F05_INSTAGRAM_CLIENT_SECRET=fixture-secret' \
        "POSTQRON_F05_INSTAGRAM_REDIRECT_URL=$callback" \
        'POSTQRON_F05_INSTAGRAM_APP_REVIEW_APPROVED=true' \
        'POSTQRON_F05_INSTAGRAM_RUNTIME_AUDIT_VERIFIED=true' \
        >> "$target"
      ;;
    threads)
      printf '%s\n' \
        'POSTQRON_F05_THREADS_ENABLED=true' \
        'POSTQRON_F05_THREADS_CLIENT_ID=fixture-client' \
        'POSTQRON_F05_THREADS_CLIENT_SECRET=fixture-secret' \
        "POSTQRON_F05_THREADS_REDIRECT_URL=$callback" \
        'POSTQRON_F05_THREADS_APP_REVIEW_APPROVED=true' \
        'POSTQRON_F05_THREADS_RUNTIME_AUDIT_VERIFIED=true' \
        >> "$target"
      ;;
    x)
      printf '%s\n' \
        'POSTQRON_F05_X_ENABLED=true' \
        'POSTQRON_F05_X_CLIENT_ID=fixture-client' \
        'POSTQRON_F05_X_CLIENT_SECRET=fixture-secret' \
        "POSTQRON_F05_X_REDIRECT_URL=$callback" \
        'POSTQRON_F05_X_API_ACCESS_APPROVED=true' \
        'POSTQRON_F05_X_RUNTIME_AUDIT_VERIFIED=true' \
        'POSTQRON_F05_X_SMOKE_TEST_VERIFIED=true' \
        >> "$target"
      ;;
    linkedin)
      printf '%s\n' \
        'POSTQRON_F05_LINKEDIN_ENABLED=true' \
        'POSTQRON_F05_LINKEDIN_CLIENT_ID=fixture-client' \
        'POSTQRON_F05_LINKEDIN_CLIENT_SECRET=fixture-secret' \
        "POSTQRON_F05_LINKEDIN_REDIRECT_URL=$callback" \
        'POSTQRON_F05_LINKEDIN_API_VERSION=202607' \
        'POSTQRON_F05_LINKEDIN_REVIEW_APPROVED=true' \
        'POSTQRON_F05_LINKEDIN_RUNTIME_AUDIT_VERIFIED=true' \
        'POSTQRON_F05_LINKEDIN_SMOKE_VERIFIED=true' \
        >> "$target"
      ;;
    pinterest)
      printf '%s\n' \
        'POSTQRON_F05_PINTEREST_ENABLED=true' \
        'POSTQRON_F05_PINTEREST_CLIENT_ID=fixture-client' \
        'POSTQRON_F05_PINTEREST_CLIENT_SECRET=fixture-secret' \
        "POSTQRON_F05_PINTEREST_REDIRECT_URL=$callback" \
        'POSTQRON_F05_PINTEREST_ACCESS_APPROVED=true' \
        'POSTQRON_F05_PINTEREST_RUNTIME_AUDIT_VERIFIED=true' \
        >> "$target"
      ;;
    tiktok)
      printf '%s\n' \
        'POSTQRON_F05_TIKTOK_ENABLED=true' \
        'POSTQRON_F05_TIKTOK_CLIENT_KEY=fixture-client' \
        'POSTQRON_F05_TIKTOK_CLIENT_SECRET=fixture-secret' \
        "POSTQRON_F05_TIKTOK_REDIRECT_URL=$callback" \
        'POSTQRON_F05_TIKTOK_CONTENT_POSTING_REVIEW_APPROVED=true' \
        'POSTQRON_F05_TIKTOK_CONTENT_POSTING_AUDIT_APPROVED=true' \
        'POSTQRON_F05_TIKTOK_RUNTIME_SMOKE_VERIFIED=true' \
        'POSTQRON_F05_TIKTOK_QUOTA_ACCESS_CONFIRMED=true' \
        >> "$target"
      ;;
    youtube)
      printf '%s\n' \
        'POSTQRON_F05_YOUTUBE_ENABLED=true' \
        'POSTQRON_F05_YOUTUBE_CLIENT_ID=fixture-client' \
        'POSTQRON_F05_YOUTUBE_CLIENT_SECRET=fixture-secret' \
        "POSTQRON_F05_YOUTUBE_REDIRECT_URL=$callback" \
        'POSTQRON_F05_YOUTUBE_OAUTH_VERIFICATION_APPROVED=true' \
        'POSTQRON_F05_YOUTUBE_API_AUDIT_APPROVED=true' \
        'POSTQRON_F05_YOUTUBE_RUNTIME_SMOKE_VERIFIED=true' \
        'POSTQRON_F05_YOUTUBE_QUOTA_ACCESS_CONFIRMED=true' \
        >> "$target"
      ;;
    google_business_profile)
      printf '%s\n' \
        'POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_ENABLED=true' \
        'POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_CLIENT_ID=fixture-client' \
        'POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_CLIENT_SECRET=fixture-secret' \
        "POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_REDIRECT_URL=$callback" \
        'POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_API_ACCESS_APPROVED=true' \
        'POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_OAUTH_REVIEW_APPROVED=true' \
        'POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_RUNTIME_AUDIT_VERIFIED=true' \
        'POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_SMOKE_VERIFIED=true' \
        >> "$target"
      ;;
    mastodon)
      printf '%s\n' \
        'POSTQRON_F05_MASTODON_ENABLED=true' \
        "POSTQRON_F05_MASTODON_REDIRECT_URL=$callback" \
        'POSTQRON_F05_MASTODON_RUNTIME_AUDIT_VERIFIED=true' \
        'POSTQRON_F05_MASTODON_RUNTIME_SMOKE_VERIFIED=true' \
        'POSTQRON_F05_MASTODON_COMPATIBILITY_VERSION=f05_dynamic_runtime_v1' \
        >> "$target"
      ;;
    bluesky)
      printf '%s\n' \
        'POSTQRON_F05_BLUESKY_ENABLED=true' \
        'POSTQRON_F05_BLUESKY_CLIENT_ID=https://postqron.com/oauth/client-metadata.json' \
        "POSTQRON_F05_BLUESKY_REDIRECT_URL=$callback" \
        'POSTQRON_F05_BLUESKY_RUNTIME_AUDIT_VERIFIED=true' \
        'POSTQRON_F05_BLUESKY_RUNTIME_SMOKE_VERIFIED=true' \
        'POSTQRON_F05_BLUESKY_COMPATIBILITY_VERSION=f05_dynamic_runtime_v1' \
        >> "$target"
      ;;
    *)
      echo "unknown test provider: $provider" >&2
      exit 1
      ;;
  esac
}

expect_failure() {
  local name=$1
  local expected=$2
  local fixture=$3
  local output
  if output=$("$validator" "$fixture" postqron.com production 2>&1); then
    echo "$name unexpectedly passed" >&2
    exit 1
  fi
  if [[ "$output" != *"$expected"* ]]; then
    echo "$name failed for an unexpected reason: $output" >&2
    exit 1
  fi
}

providers=(
  facebook_pages
  instagram_professional
  threads
  x
  linkedin
  pinterest
  tiktok
  youtube
  google_business_profile
  mastodon
  bluesky
)
for provider in "${providers[@]}"; do
  fixture="$temporary_dir/$provider.env"
  write_base "$fixture"
  append_ready_provider "$provider" "$fixture"
  output=$("$validator" "$fixture" postqron.com production)
  if [[ "$output" != *"$provider"* || "$output" != *"$callback"* ]]; then
    echo "$provider did not produce a safe validation summary" >&2
    exit 1
  fi
done

no_provider="$temporary_dir/no-provider.env"
write_base "$no_provider"
no_provider_output=$("$validator" "$no_provider" postqron.com production)
if [[ "$no_provider_output" != *"catalog remains fail-closed"* ]]; then
  echo "provider-free configuration did not report fail-closed state" >&2
  exit 1
fi

first_smoke_canary="$temporary_dir/x-first-smoke-canary.env"
write_base "$first_smoke_canary"
printf '%s\n' \
  'POSTQRON_F05_X_ENABLED=true' \
  'POSTQRON_F05_X_CLIENT_ID=fixture-client' \
  'POSTQRON_F05_X_CLIENT_SECRET=fixture-secret' \
  "POSTQRON_F05_X_REDIRECT_URL=$callback" \
  'POSTQRON_F05_X_API_ACCESS_APPROVED=true' \
  'POSTQRON_F05_X_RUNTIME_AUDIT_VERIFIED=true' \
  'POSTQRON_F05_X_SMOKE_TEST_VERIFIED=false' \
  'POSTQRON_F05_X_FIRST_SMOKE_CANARY_ENABLED=true' \
  'POSTQRON_F05_X_FIRST_SMOKE_CANARY_WORKSPACE_ID=workspace-canary' \
  'POSTQRON_F05_X_FIRST_SMOKE_CANARY_ACTOR_ACCOUNT_ID=actor-canary' \
  "POSTQRON_F05_X_FIRST_SMOKE_CANARY_EXPIRES_AT=$canary_expires_at" \
  >> "$first_smoke_canary"
first_smoke_output=$(
  "$validator" "$first_smoke_canary" postqron.com production
)
if [[ "$first_smoke_output" != *"fail-closed outside the scoped canary"* ]]; then
  echo "first-smoke canary did not report its restricted catalog state" >&2
  exit 1
fi

missing_canary="$temporary_dir/x-missing-canary.env"
cp "$first_smoke_canary" "$missing_canary"
sed -i.bak \
  '/^POSTQRON_F05_X_FIRST_SMOKE_CANARY_ACTOR_ACCOUNT_ID=/d' \
  "$missing_canary"
expect_failure missing-canary \
  "POSTQRON_F05_X_FIRST_SMOKE_CANARY_ACTOR_ACCOUNT_ID is missing" \
  "$missing_canary"

verified_with_canary="$temporary_dir/x-verified-with-canary.env"
cp "$first_smoke_canary" "$verified_with_canary"
sed -i.bak \
  's/POSTQRON_F05_X_SMOKE_TEST_VERIFIED=false/POSTQRON_F05_X_SMOKE_TEST_VERIFIED=true/' \
  "$verified_with_canary"
expect_failure verified-with-canary \
  "first-smoke canary must be removed" \
  "$verified_with_canary"

overlong_canary="$temporary_dir/x-overlong-canary.env"
cp "$first_smoke_canary" "$overlong_canary"
sed -i.bak \
  "s/$canary_expires_at/$canary_too_long_at/" \
  "$overlong_canary"
expect_failure overlong-canary \
  "no more than two hours away" \
  "$overlong_canary"

partial="$temporary_dir/partial.env"
write_base "$partial"
printf '%s\n' 'POSTQRON_F05_X_ENABLED=true' >> "$partial"
expect_failure partial-provider "POSTQRON_F05_X_CLIENT_ID is missing" "$partial"

wrong_redirect="$temporary_dir/wrong-redirect.env"
write_base "$wrong_redirect"
append_ready_provider x "$wrong_redirect"
sed -i.bak 's#https://postqron.com/app/social-oauth/callback#https://api.postqron.com/api/v1/social-authorizations/callback#' "$wrong_redirect"
expect_failure wrong-redirect "redirect must equal $callback" "$wrong_redirect"

unknown_key="$temporary_dir/unknown.env"
write_base "$unknown_key"
append_ready_provider x "$unknown_key"
printf '%s\n' 'POSTQRON_F05_X_CLIENT_SECRETT=typo' >> "$unknown_key"
expect_failure unknown-key "unknown F5 runtime key" "$unknown_key"

duplicate_key="$temporary_dir/duplicate.env"
write_base "$duplicate_key"
append_ready_provider x "$duplicate_key"
printf '%s\n' 'POSTQRON_F05_X_ENABLED=true' >> "$duplicate_key"
expect_failure duplicate-key "duplicate F5 runtime key" "$duplicate_key"

malformed_key="$temporary_dir/malformed.env"
write_base "$malformed_key"
append_ready_provider x "$malformed_key"
printf '%s\n' 'export POSTQRON_F05_PINTEREST_ENABLED=true' >> "$malformed_key"
expect_failure malformed-key "malformed F5 runtime entry" "$malformed_key"

invalid_cipher="$temporary_dir/invalid-cipher.env"
write_base "$invalid_cipher"
append_ready_provider x "$invalid_cipher"
sed -i.bak \
  's/^POSTQRON_F05_CIPHER_KEY_BASE64=.*/POSTQRON_F05_CIPHER_KEY_BASE64=not-base64/' \
  "$invalid_cipher"
expect_failure invalid-cipher "must encode exactly 32 bytes" "$invalid_cipher"

invalid_meta_version="$temporary_dir/invalid-meta-version.env"
write_base "$invalid_meta_version"
append_ready_provider facebook_pages "$invalid_meta_version"
sed -i.bak 's/POSTQRON_F05_META_GRAPH_VERSION=v25.0/POSTQRON_F05_META_GRAPH_VERSION=latest/' \
  "$invalid_meta_version"
expect_failure invalid-meta-version "Meta Graph version" "$invalid_meta_version"

invalid_bluesky_client="$temporary_dir/invalid-bluesky-client.env"
write_base "$invalid_bluesky_client"
append_ready_provider bluesky "$invalid_bluesky_client"
sed -i.bak \
  's#POSTQRON_F05_BLUESKY_CLIENT_ID=https://postqron.com/oauth/client-metadata.json#POSTQRON_F05_BLUESKY_CLIENT_ID=https://#' \
  "$invalid_bluesky_client"
expect_failure invalid-bluesky-client "canonical HTTPS URL" "$invalid_bluesky_client"

invalid_boolean="$temporary_dir/invalid-boolean.env"
write_base "$invalid_boolean"
append_ready_provider x "$invalid_boolean"
sed -i.bak 's/POSTQRON_F05_X_ENABLED=true/POSTQRON_F05_X_ENABLED=TRUE/' \
  "$invalid_boolean"
expect_failure invalid-boolean "must be exactly true or false" "$invalid_boolean"

staging="$temporary_dir/staging.env"
write_base "$staging"
append_ready_provider x "$staging"
sed -i.bak \
  's#https://postqron.com/app/social-oauth/callback#https://staging.example.com/app/social-oauth/callback#' \
  "$staging"
"$validator" "$staging" staging.example.com staging >/dev/null

redaction="$temporary_dir/redaction.env"
write_base "$redaction"
printf '%s\n' \
  'POSTQRON_F05_X_ENABLED=true' \
  'POSTQRON_F05_X_CLIENT_SECRET=NEVER_PRINT_THIS_FIXTURE_VALUE' \
  >> "$redaction"
if redaction_output=$("$validator" "$redaction" postqron.com production 2>&1); then
  echo "redaction fixture unexpectedly passed" >&2
  exit 1
fi
if [[ "$redaction_output" == *NEVER_PRINT_THIS_FIXTURE_VALUE* ]]; then
  echo "validator exposed a configured secret value" >&2
  exit 1
fi

staging_legacy="$temporary_dir/staging-legacy-runtime.env"
printf '%s\n' 'UNRELATED_RUNTIME_VALUE=staging-opaque' > "$staging_legacy"
cp "$staging_legacy" "$temporary_dir/staging-legacy.before"
env -i PATH="$PATH" \
  "$composer" "$staging_legacy" staging.example.com staging >/dev/null
if ! cmp --silent \
  "$temporary_dir/staging-legacy.before" \
  "$staging_legacy"; then
  echo "provider-free staging composition changed legacy RUNTIME_ENV" >&2
  exit 1
fi

legacy_runtime="$temporary_dir/legacy-runtime.env"
printf '%s\n' \
  'DATABASE_URL=postgres://legacy-preserved' \
  'UNRELATED_RUNTIME_VALUE=opaque-legacy-value' \
  > "$legacy_runtime"
composition_output=$(
  compose_x_runtime \
    "$legacy_runtime" \
    "$cipher_key" \
    false \
    true \
    workspace-canary \
    actor-canary \
    "$canary_expires_at" 2>&1
)
if ! grep --fixed-strings --line-regexp --quiet \
  'UNRELATED_RUNTIME_VALUE=opaque-legacy-value' "$legacy_runtime"; then
  echo "dedicated composition did not preserve legacy RUNTIME_ENV" >&2
  exit 1
fi
if [[ "$composition_output" == *NEVER_PRINT_DEDICATED_SECRET* ]]; then
  echo "dedicated composition exposed an X secret" >&2
  exit 1
fi
if [[ $(grep --count '^POSTQRON_F05_X_CLIENT_ID=' "$legacy_runtime") != 1 ]]; then
  echo "dedicated composition did not append exactly one X client ID" >&2
  exit 1
fi

post_smoke_runtime="$temporary_dir/post-smoke-runtime.env"
printf '%s\n' 'UNRELATED_RUNTIME_VALUE=still-opaque' > "$post_smoke_runtime"
compose_x_runtime \
  "$post_smoke_runtime" \
  "$cipher_key" \
  true \
  false \
  '' \
  '' \
  '' \
  >/dev/null

conflicting_runtime="$temporary_dir/conflicting-runtime.env"
printf '%s\n' \
  'UNRELATED_RUNTIME_VALUE=preserve-on-failure' \
  'POSTQRON_F05_X_CLIENT_ID=legacy-client' \
  > "$conflicting_runtime"
cp "$conflicting_runtime" "$temporary_dir/conflicting-runtime.before"
if conflict_output=$(
  compose_x_runtime \
    "$conflicting_runtime" \
    "$cipher_key" \
    false \
    true \
    workspace-canary \
    actor-canary \
    "$canary_expires_at" 2>&1
); then
  echo "conflicting legacy and dedicated F5 keys unexpectedly composed" >&2
  exit 1
fi
if [[ "$conflict_output" != *"conflicts with dedicated F5 configuration"* ]] ||
  [[ "$conflict_output" == *NEVER_PRINT_DEDICATED_SECRET* ]]; then
  echo "F5 conflict failed unsafely: $conflict_output" >&2
  exit 1
fi
if ! cmp --silent \
  "$temporary_dir/conflicting-runtime.before" \
  "$conflicting_runtime"; then
  echo "failed F5 composition mutated legacy RUNTIME_ENV" >&2
  exit 1
fi

missing_cipher_runtime="$temporary_dir/missing-cipher-runtime.env"
printf '%s\n' 'UNRELATED_RUNTIME_VALUE=preserved' > "$missing_cipher_runtime"
if missing_cipher_output=$(
  compose_x_runtime \
    "$missing_cipher_runtime" \
    '' \
    false \
    true \
    workspace-canary \
    actor-canary \
    "$canary_expires_at" 2>&1
); then
  echo "production composition without the dedicated cipher unexpectedly passed" >&2
  exit 1
fi
if [[ "$missing_cipher_output" != *"POSTQRON_F05_CIPHER_KEY_BASE64"* ]] ||
  [[ "$missing_cipher_output" == *NEVER_PRINT_DEDICATED_SECRET* ]]; then
  echo "missing cipher failed for an unsafe reason: $missing_cipher_output" >&2
  exit 1
fi

grep -Eo 'POSTQRON_F05_[A-Z0-9_]+' "$validator" | sort -u \
  > "$temporary_dir/validator-keys"
grep -Eo 'POSTQRON_F05_[A-Z0-9_]+' "$compose" | sort -u \
  > "$temporary_dir/compose-keys"
if ! diff -u "$temporary_dir/validator-keys" "$temporary_dir/compose-keys"; then
  echo "Compose and the F5 validator do not expose the same runtime inventory" >&2
  exit 1
fi

find "$script_dir/../../features/f05-social-connections" \
  -maxdepth 1 -name '*.go' ! -name '*_test.go' -exec \
  grep -hEo 'POSTQRON_F05_[A-Z0-9_]+' {} + | sort -u \
  > "$temporary_dir/runtime-keys"
if ! diff -u "$temporary_dir/runtime-keys" "$temporary_dir/validator-keys"; then
  echo "F5 runtime and deploy validator do not use the same key inventory" >&2
  exit 1
fi

if ! grep -Fq './infra/deploy/compose-f05-runtime.sh' "$workflow"; then
  echo "Deploy workflow does not invoke atomic F5 composition" >&2
  exit 1
fi

for secret_name in \
  POSTQRON_F05_X_CLIENT_ID \
  POSTQRON_F05_X_CLIENT_SECRET \
  POSTQRON_F05_CIPHER_KEY_BASE64; do
  if ! grep -Fq "$secret_name: \${{ secrets.$secret_name }}" "$workflow"; then
    echo "Deploy workflow does not use the dedicated $secret_name secret" >&2
    exit 1
  fi
done

echo "F5 deploy validation and composition tests passed"
