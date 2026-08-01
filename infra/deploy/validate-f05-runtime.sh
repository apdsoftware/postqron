#!/usr/bin/env bash

set -euo pipefail

if (( $# != 3 )); then
  echo "usage: validate-f05-runtime.sh RUNTIME_ENV APP_DOMAIN ENVIRONMENT" >&2
  exit 2
fi

runtime_file=$1
app_domain=$2
deployment_environment=$3

if [[ ! -r "$runtime_file" ]]; then
  echo "F5 runtime configuration file is not readable" >&2
  exit 1
fi
if [[ ! "$app_domain" =~ ^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$ ]]; then
  echo "APP_DOMAIN is not a canonical DNS name" >&2
  exit 1
fi
if [[ "$deployment_environment" != "staging" &&
  "$deployment_environment" != "production" ]]; then
  echo "deployment environment must be staging or production" >&2
  exit 1
fi
if [[ "$deployment_environment" == "production" &&
  "$app_domain" != "postqron.com" ]]; then
  echo "production APP_DOMAIN must be postqron.com" >&2
  exit 1
fi

readonly expected_callback="https://${app_domain}/app/social-oauth/callback"

known_keys=(
  POSTQRON_F05_ENABLED
  POSTQRON_F05_CIPHER_KEY_ID
  POSTQRON_F05_CIPHER_KEY_BASE64
  POSTQRON_F05_META_ENABLED
  POSTQRON_F05_META_GRAPH_VERSION
  POSTQRON_F05_FACEBOOK_CLIENT_ID
  POSTQRON_F05_FACEBOOK_CLIENT_SECRET
  POSTQRON_F05_FACEBOOK_REDIRECT_URL
  POSTQRON_F05_FACEBOOK_LOGIN_CONFIG_ID
  POSTQRON_F05_FACEBOOK_APP_REVIEW_APPROVED
  POSTQRON_F05_FACEBOOK_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_INSTAGRAM_CLIENT_ID
  POSTQRON_F05_INSTAGRAM_CLIENT_SECRET
  POSTQRON_F05_INSTAGRAM_REDIRECT_URL
  POSTQRON_F05_INSTAGRAM_APP_REVIEW_APPROVED
  POSTQRON_F05_INSTAGRAM_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_THREADS_ENABLED
  POSTQRON_F05_THREADS_CLIENT_ID
  POSTQRON_F05_THREADS_CLIENT_SECRET
  POSTQRON_F05_THREADS_REDIRECT_URL
  POSTQRON_F05_THREADS_APP_REVIEW_APPROVED
  POSTQRON_F05_THREADS_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_X_ENABLED
  POSTQRON_F05_X_CLIENT_ID
  POSTQRON_F05_X_CLIENT_SECRET
  POSTQRON_F05_X_REDIRECT_URL
  POSTQRON_F05_X_API_ACCESS_APPROVED
  POSTQRON_F05_X_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_X_SMOKE_TEST_VERIFIED
  POSTQRON_F05_X_FIRST_SMOKE_CANARY_ENABLED
  POSTQRON_F05_X_FIRST_SMOKE_CANARY_WORKSPACE_ID
  POSTQRON_F05_X_FIRST_SMOKE_CANARY_ACTOR_ACCOUNT_ID
  POSTQRON_F05_X_FIRST_SMOKE_CANARY_EXPIRES_AT
  POSTQRON_F05_LINKEDIN_ENABLED
  POSTQRON_F05_LINKEDIN_CLIENT_ID
  POSTQRON_F05_LINKEDIN_CLIENT_SECRET
  POSTQRON_F05_LINKEDIN_REDIRECT_URL
  POSTQRON_F05_LINKEDIN_API_VERSION
  POSTQRON_F05_LINKEDIN_REVIEW_APPROVED
  POSTQRON_F05_LINKEDIN_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_LINKEDIN_SMOKE_VERIFIED
  POSTQRON_F05_LINKEDIN_PROGRAMMATIC_REFRESH_APPROVED
  POSTQRON_F05_PINTEREST_ENABLED
  POSTQRON_F05_PINTEREST_CLIENT_ID
  POSTQRON_F05_PINTEREST_CLIENT_SECRET
  POSTQRON_F05_PINTEREST_REDIRECT_URL
  POSTQRON_F05_PINTEREST_ACCESS_APPROVED
  POSTQRON_F05_PINTEREST_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_TIKTOK_ENABLED
  POSTQRON_F05_TIKTOK_CLIENT_KEY
  POSTQRON_F05_TIKTOK_CLIENT_SECRET
  POSTQRON_F05_TIKTOK_REDIRECT_URL
  POSTQRON_F05_TIKTOK_CONTENT_POSTING_REVIEW_APPROVED
  POSTQRON_F05_TIKTOK_CONTENT_POSTING_AUDIT_APPROVED
  POSTQRON_F05_TIKTOK_RUNTIME_SMOKE_VERIFIED
  POSTQRON_F05_TIKTOK_QUOTA_ACCESS_CONFIRMED
  POSTQRON_F05_YOUTUBE_ENABLED
  POSTQRON_F05_YOUTUBE_CLIENT_ID
  POSTQRON_F05_YOUTUBE_CLIENT_SECRET
  POSTQRON_F05_YOUTUBE_REDIRECT_URL
  POSTQRON_F05_YOUTUBE_OAUTH_VERIFICATION_APPROVED
  POSTQRON_F05_YOUTUBE_API_AUDIT_APPROVED
  POSTQRON_F05_YOUTUBE_RUNTIME_SMOKE_VERIFIED
  POSTQRON_F05_YOUTUBE_QUOTA_ACCESS_CONFIRMED
  POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_ENABLED
  POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_CLIENT_ID
  POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_CLIENT_SECRET
  POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_REDIRECT_URL
  POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_API_ACCESS_APPROVED
  POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_OAUTH_REVIEW_APPROVED
  POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_SMOKE_VERIFIED
  POSTQRON_F05_MASTODON_ENABLED
  POSTQRON_F05_MASTODON_REDIRECT_URL
  POSTQRON_F05_MASTODON_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_MASTODON_RUNTIME_SMOKE_VERIFIED
  POSTQRON_F05_MASTODON_COMPATIBILITY_VERSION
  POSTQRON_F05_BLUESKY_ENABLED
  POSTQRON_F05_BLUESKY_CLIENT_ID
  POSTQRON_F05_BLUESKY_REDIRECT_URL
  POSTQRON_F05_BLUESKY_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_BLUESKY_RUNTIME_SMOKE_VERIFIED
  POSTQRON_F05_BLUESKY_COMPATIBILITY_VERSION
  POSTQRON_F05_BLUESKY_PLC_DIRECTORY_ORIGIN
)

configured_keys=()
configured_values=()

is_known_key() {
  local requested=$1
  local candidate
  for candidate in "${known_keys[@]}"; do
    if [[ "$candidate" == "$requested" ]]; then
      return 0
    fi
  done
  return 1
}

key_index() {
  local requested=$1
  local index
  for (( index = 0; index < ${#configured_keys[@]}; index++ )); do
    if [[ "${configured_keys[$index]}" == "$requested" ]]; then
      printf '%s' "$index"
      return 0
    fi
  done
  return 1
}

value_of() {
  local requested=$1
  local index
  if index=$(key_index "$requested"); then
    printf '%s' "${configured_values[$index]}"
  fi
}

while IFS= read -r line || [[ -n "$line" ]]; do
  line=${line%$'\r'}
  if [[ "$line" =~ ^[[:space:]]*$ || "$line" =~ ^[[:space:]]*# ]]; then
    continue
  fi
  if [[ "$line" != *=* ]]; then
    if [[ "$line" == POSTQRON_F05_* ]]; then
      echo "F5 runtime entry is missing '='" >&2
      exit 1
    fi
    continue
  fi
  key=${line%%=*}
  value=${line#*=}
  if [[ "$key" != POSTQRON_F05_* ]]; then
    if [[ "$line" == *POSTQRON_F05_* ]]; then
      echo "malformed F5 runtime entry" >&2
      exit 1
    fi
    continue
  fi
  if [[ ! "$key" =~ ^[A-Z0-9_]+$ ]] || ! is_known_key "$key"; then
    echo "unknown F5 runtime key: $key" >&2
    exit 1
  fi
  if key_index "$key" >/dev/null; then
    echo "duplicate F5 runtime key: $key" >&2
    exit 1
  fi
  if [[ "$value" =~ ^[[:space:]] || "$value" =~ [[:space:]]$ ]]; then
    echo "F5 runtime value has surrounding whitespace: $key" >&2
    exit 1
  fi
  configured_keys+=("$key")
  configured_values+=("$value")
done < "$runtime_file"

boolean_keys=(
  POSTQRON_F05_ENABLED
  POSTQRON_F05_META_ENABLED
  POSTQRON_F05_FACEBOOK_APP_REVIEW_APPROVED
  POSTQRON_F05_FACEBOOK_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_INSTAGRAM_APP_REVIEW_APPROVED
  POSTQRON_F05_INSTAGRAM_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_THREADS_ENABLED
  POSTQRON_F05_THREADS_APP_REVIEW_APPROVED
  POSTQRON_F05_THREADS_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_X_ENABLED
  POSTQRON_F05_X_API_ACCESS_APPROVED
  POSTQRON_F05_X_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_X_SMOKE_TEST_VERIFIED
  POSTQRON_F05_X_FIRST_SMOKE_CANARY_ENABLED
  POSTQRON_F05_LINKEDIN_ENABLED
  POSTQRON_F05_LINKEDIN_REVIEW_APPROVED
  POSTQRON_F05_LINKEDIN_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_LINKEDIN_SMOKE_VERIFIED
  POSTQRON_F05_LINKEDIN_PROGRAMMATIC_REFRESH_APPROVED
  POSTQRON_F05_PINTEREST_ENABLED
  POSTQRON_F05_PINTEREST_ACCESS_APPROVED
  POSTQRON_F05_PINTEREST_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_TIKTOK_ENABLED
  POSTQRON_F05_TIKTOK_CONTENT_POSTING_REVIEW_APPROVED
  POSTQRON_F05_TIKTOK_CONTENT_POSTING_AUDIT_APPROVED
  POSTQRON_F05_TIKTOK_RUNTIME_SMOKE_VERIFIED
  POSTQRON_F05_TIKTOK_QUOTA_ACCESS_CONFIRMED
  POSTQRON_F05_YOUTUBE_ENABLED
  POSTQRON_F05_YOUTUBE_OAUTH_VERIFICATION_APPROVED
  POSTQRON_F05_YOUTUBE_API_AUDIT_APPROVED
  POSTQRON_F05_YOUTUBE_RUNTIME_SMOKE_VERIFIED
  POSTQRON_F05_YOUTUBE_QUOTA_ACCESS_CONFIRMED
  POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_ENABLED
  POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_API_ACCESS_APPROVED
  POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_OAUTH_REVIEW_APPROVED
  POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_SMOKE_VERIFIED
  POSTQRON_F05_MASTODON_ENABLED
  POSTQRON_F05_MASTODON_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_MASTODON_RUNTIME_SMOKE_VERIFIED
  POSTQRON_F05_BLUESKY_ENABLED
  POSTQRON_F05_BLUESKY_RUNTIME_AUDIT_VERIFIED
  POSTQRON_F05_BLUESKY_RUNTIME_SMOKE_VERIFIED
)
for key in "${boolean_keys[@]}"; do
  value=$(value_of "$key")
  if [[ -n "$value" && "$value" != "true" && "$value" != "false" ]]; then
    echo "$key must be exactly true or false" >&2
    exit 1
  fi
done

require_values() {
  local provider=$1
  shift
  local key
  for key in "$@"; do
    if [[ -z "$(value_of "$key")" ]]; then
      echo "$provider is enabled but $key is missing" >&2
      exit 1
    fi
  done
}

require_true() {
  local provider=$1
  shift
  local key
  for key in "$@"; do
    if [[ "$(value_of "$key")" != "true" ]]; then
      echo "$provider is enabled but $key is not true" >&2
      exit 1
    fi
  done
}

require_callback() {
  local provider=$1
  local key=$2
  if [[ "$(value_of "$key")" != "$expected_callback" ]]; then
    echo "$provider redirect must equal $expected_callback" >&2
    exit 1
  fi
}

require_meta_version() {
  local provider=$1
  if [[ ! "$(value_of POSTQRON_F05_META_GRAPH_VERSION)" =~ ^v[0-9]+\.[0-9]+$ ]]; then
    echo "$provider requires a Meta Graph version such as v25.0" >&2
    exit 1
  fi
}

require_future_utc_timestamp() {
  local key=$1
  local value
  local epoch
  local now_epoch
  value=$(value_of "$key")
  if [[ ! "$value" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]]; then
    echo "$key must be an RFC3339 UTC timestamp" >&2
    exit 1
  fi
  if epoch=$(date -u -d "$value" +%s 2>/dev/null); then
    :
  elif epoch=$(date -j -u -f '%Y-%m-%dT%H:%M:%SZ' "$value" '+%s' 2>/dev/null); then
    :
  else
    echo "$key must be a valid RFC3339 UTC timestamp" >&2
    exit 1
  fi
  now_epoch=$(date -u +%s)
  if (( epoch <= now_epoch || epoch > now_epoch + 7200 )); then
    echo "$key must be in the future and no more than two hours away" >&2
    exit 1
  fi
}

any_present() {
  local key
  for key in "$@"; do
    if [[ -n "$(value_of "$key")" ]]; then
      return 0
    fi
  done
  return 1
}

configured_providers=()
first_smoke_canary=false

facebook_keys=(
  POSTQRON_F05_FACEBOOK_CLIENT_ID
  POSTQRON_F05_FACEBOOK_CLIENT_SECRET
  POSTQRON_F05_FACEBOOK_REDIRECT_URL
  POSTQRON_F05_FACEBOOK_LOGIN_CONFIG_ID
)
if any_present "${facebook_keys[@]}"; then
  require_true facebook_pages POSTQRON_F05_META_ENABLED
  require_values facebook_pages POSTQRON_F05_META_GRAPH_VERSION \
    POSTQRON_F05_FACEBOOK_CLIENT_ID POSTQRON_F05_FACEBOOK_CLIENT_SECRET \
    POSTQRON_F05_FACEBOOK_REDIRECT_URL POSTQRON_F05_FACEBOOK_LOGIN_CONFIG_ID
  require_true facebook_pages POSTQRON_F05_FACEBOOK_APP_REVIEW_APPROVED \
    POSTQRON_F05_FACEBOOK_RUNTIME_AUDIT_VERIFIED
  require_meta_version facebook_pages
  require_callback facebook_pages POSTQRON_F05_FACEBOOK_REDIRECT_URL
  configured_providers+=(facebook_pages)
fi

instagram_keys=(
  POSTQRON_F05_INSTAGRAM_CLIENT_ID
  POSTQRON_F05_INSTAGRAM_CLIENT_SECRET
  POSTQRON_F05_INSTAGRAM_REDIRECT_URL
)
if any_present "${instagram_keys[@]}"; then
  require_true instagram_professional POSTQRON_F05_META_ENABLED
  require_values instagram_professional POSTQRON_F05_META_GRAPH_VERSION \
    POSTQRON_F05_INSTAGRAM_CLIENT_ID POSTQRON_F05_INSTAGRAM_CLIENT_SECRET \
    POSTQRON_F05_INSTAGRAM_REDIRECT_URL
  require_true instagram_professional POSTQRON_F05_INSTAGRAM_APP_REVIEW_APPROVED \
    POSTQRON_F05_INSTAGRAM_RUNTIME_AUDIT_VERIFIED
  require_meta_version instagram_professional
  require_callback instagram_professional POSTQRON_F05_INSTAGRAM_REDIRECT_URL
  configured_providers+=(instagram_professional)
fi

if [[ "$(value_of POSTQRON_F05_THREADS_ENABLED)" == "true" ]]; then
  require_values threads POSTQRON_F05_THREADS_CLIENT_ID \
    POSTQRON_F05_THREADS_CLIENT_SECRET POSTQRON_F05_THREADS_REDIRECT_URL
  require_true threads POSTQRON_F05_THREADS_APP_REVIEW_APPROVED \
    POSTQRON_F05_THREADS_RUNTIME_AUDIT_VERIFIED
  require_callback threads POSTQRON_F05_THREADS_REDIRECT_URL
  configured_providers+=(threads)
fi

if [[ "$(value_of POSTQRON_F05_X_ENABLED)" == "true" ]]; then
  require_values x POSTQRON_F05_X_CLIENT_ID POSTQRON_F05_X_CLIENT_SECRET \
    POSTQRON_F05_X_REDIRECT_URL
  require_true x POSTQRON_F05_X_API_ACCESS_APPROVED \
    POSTQRON_F05_X_RUNTIME_AUDIT_VERIFIED
  require_callback x POSTQRON_F05_X_REDIRECT_URL
  x_smoke_verified=$(value_of POSTQRON_F05_X_SMOKE_TEST_VERIFIED)
  if [[ "$x_smoke_verified" == "true" ]]; then
    if [[ "$(value_of POSTQRON_F05_X_FIRST_SMOKE_CANARY_ENABLED)" == "true" ]] ||
      any_present POSTQRON_F05_X_FIRST_SMOKE_CANARY_WORKSPACE_ID \
        POSTQRON_F05_X_FIRST_SMOKE_CANARY_ACTOR_ACCOUNT_ID \
        POSTQRON_F05_X_FIRST_SMOKE_CANARY_EXPIRES_AT; then
      echo "X first-smoke canary must be removed after smoke verification" >&2
      exit 1
    fi
    configured_providers+=(x)
  elif [[ "$x_smoke_verified" == "false" ]]; then
    require_true x_first_smoke_canary \
      POSTQRON_F05_X_FIRST_SMOKE_CANARY_ENABLED
    require_values x_first_smoke_canary \
      POSTQRON_F05_X_FIRST_SMOKE_CANARY_WORKSPACE_ID \
      POSTQRON_F05_X_FIRST_SMOKE_CANARY_ACTOR_ACCOUNT_ID \
      POSTQRON_F05_X_FIRST_SMOKE_CANARY_EXPIRES_AT
    require_future_utc_timestamp \
      POSTQRON_F05_X_FIRST_SMOKE_CANARY_EXPIRES_AT
    configured_providers+=(x_first_smoke_canary)
    first_smoke_canary=true
  else
    echo "x is enabled but POSTQRON_F05_X_SMOKE_TEST_VERIFIED is missing" >&2
    exit 1
  fi
fi

if [[ "$(value_of POSTQRON_F05_LINKEDIN_ENABLED)" == "true" ]]; then
  require_values linkedin POSTQRON_F05_LINKEDIN_CLIENT_ID \
    POSTQRON_F05_LINKEDIN_CLIENT_SECRET POSTQRON_F05_LINKEDIN_REDIRECT_URL \
    POSTQRON_F05_LINKEDIN_API_VERSION
  if [[ ! "$(value_of POSTQRON_F05_LINKEDIN_API_VERSION)" =~ ^[0-9]{6}$ ]]; then
    echo "POSTQRON_F05_LINKEDIN_API_VERSION must use YYYYMM" >&2
    exit 1
  fi
  require_true linkedin POSTQRON_F05_LINKEDIN_REVIEW_APPROVED \
    POSTQRON_F05_LINKEDIN_RUNTIME_AUDIT_VERIFIED \
    POSTQRON_F05_LINKEDIN_SMOKE_VERIFIED
  require_callback linkedin POSTQRON_F05_LINKEDIN_REDIRECT_URL
  configured_providers+=(linkedin)
fi

if [[ "$(value_of POSTQRON_F05_PINTEREST_ENABLED)" == "true" ]]; then
  require_values pinterest POSTQRON_F05_PINTEREST_CLIENT_ID \
    POSTQRON_F05_PINTEREST_CLIENT_SECRET POSTQRON_F05_PINTEREST_REDIRECT_URL
  require_true pinterest POSTQRON_F05_PINTEREST_ACCESS_APPROVED \
    POSTQRON_F05_PINTEREST_RUNTIME_AUDIT_VERIFIED
  require_callback pinterest POSTQRON_F05_PINTEREST_REDIRECT_URL
  configured_providers+=(pinterest)
fi

if [[ "$(value_of POSTQRON_F05_TIKTOK_ENABLED)" == "true" ]]; then
  require_values tiktok POSTQRON_F05_TIKTOK_CLIENT_KEY \
    POSTQRON_F05_TIKTOK_CLIENT_SECRET POSTQRON_F05_TIKTOK_REDIRECT_URL
  require_true tiktok POSTQRON_F05_TIKTOK_CONTENT_POSTING_REVIEW_APPROVED \
    POSTQRON_F05_TIKTOK_CONTENT_POSTING_AUDIT_APPROVED \
    POSTQRON_F05_TIKTOK_RUNTIME_SMOKE_VERIFIED \
    POSTQRON_F05_TIKTOK_QUOTA_ACCESS_CONFIRMED
  require_callback tiktok POSTQRON_F05_TIKTOK_REDIRECT_URL
  configured_providers+=(tiktok)
fi

if [[ "$(value_of POSTQRON_F05_YOUTUBE_ENABLED)" == "true" ]]; then
  require_values youtube POSTQRON_F05_YOUTUBE_CLIENT_ID \
    POSTQRON_F05_YOUTUBE_CLIENT_SECRET POSTQRON_F05_YOUTUBE_REDIRECT_URL
  require_true youtube POSTQRON_F05_YOUTUBE_OAUTH_VERIFICATION_APPROVED \
    POSTQRON_F05_YOUTUBE_API_AUDIT_APPROVED \
    POSTQRON_F05_YOUTUBE_RUNTIME_SMOKE_VERIFIED \
    POSTQRON_F05_YOUTUBE_QUOTA_ACCESS_CONFIRMED
  require_callback youtube POSTQRON_F05_YOUTUBE_REDIRECT_URL
  configured_providers+=(youtube)
fi

if [[ "$(value_of POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_ENABLED)" == "true" ]]; then
  require_values google_business_profile \
    POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_CLIENT_ID \
    POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_CLIENT_SECRET \
    POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_REDIRECT_URL
  require_true google_business_profile \
    POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_API_ACCESS_APPROVED \
    POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_OAUTH_REVIEW_APPROVED \
    POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_RUNTIME_AUDIT_VERIFIED \
    POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_SMOKE_VERIFIED
  require_callback google_business_profile \
    POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_REDIRECT_URL
  configured_providers+=(google_business_profile)
fi

if [[ "$(value_of POSTQRON_F05_MASTODON_ENABLED)" == "true" ]]; then
  require_values mastodon POSTQRON_F05_MASTODON_REDIRECT_URL \
    POSTQRON_F05_MASTODON_COMPATIBILITY_VERSION
  require_true mastodon POSTQRON_F05_MASTODON_RUNTIME_AUDIT_VERIFIED \
    POSTQRON_F05_MASTODON_RUNTIME_SMOKE_VERIFIED
  if [[ "$(value_of POSTQRON_F05_MASTODON_COMPATIBILITY_VERSION)" != \
    "f05_dynamic_runtime_v1" ]]; then
    echo "Mastodon compatibility version is not supported" >&2
    exit 1
  fi
  require_callback mastodon POSTQRON_F05_MASTODON_REDIRECT_URL
  configured_providers+=(mastodon)
fi

if [[ "$(value_of POSTQRON_F05_BLUESKY_ENABLED)" == "true" ]]; then
  require_values bluesky POSTQRON_F05_BLUESKY_CLIENT_ID \
    POSTQRON_F05_BLUESKY_REDIRECT_URL \
    POSTQRON_F05_BLUESKY_COMPATIBILITY_VERSION
  require_true bluesky POSTQRON_F05_BLUESKY_RUNTIME_AUDIT_VERIFIED \
    POSTQRON_F05_BLUESKY_RUNTIME_SMOKE_VERIFIED
  if [[ "$(value_of POSTQRON_F05_BLUESKY_COMPATIBILITY_VERSION)" != \
    "f05_dynamic_runtime_v1" ]]; then
    echo "Bluesky compatibility version is not supported" >&2
    exit 1
  fi
  if [[ ! "$(value_of POSTQRON_F05_BLUESKY_CLIENT_ID)" =~ ^https://[A-Za-z0-9.-]+(/[A-Za-z0-9._~!$\&\'\(\)*+,\;=:@%/-]*)?$ ]]; then
    echo "POSTQRON_F05_BLUESKY_CLIENT_ID must be a canonical HTTPS URL" >&2
    exit 1
  fi
  if [[ -n "$(value_of POSTQRON_F05_BLUESKY_PLC_DIRECTORY_ORIGIN)" &&
    ! "$(value_of POSTQRON_F05_BLUESKY_PLC_DIRECTORY_ORIGIN)" =~ ^https://[A-Za-z0-9.-]+$ ]]; then
    echo "POSTQRON_F05_BLUESKY_PLC_DIRECTORY_ORIGIN must be an HTTPS origin" >&2
    exit 1
  fi
  require_callback bluesky POSTQRON_F05_BLUESKY_REDIRECT_URL
  configured_providers+=(bluesky)
fi

if (( ${#configured_providers[@]} == 0 )); then
  printf 'F5 configuration has no enabled provider; catalog remains fail-closed; canonical callback: %s\n' \
    "$expected_callback"
  exit 0
fi
require_true f05 POSTQRON_F05_ENABLED
require_values f05 POSTQRON_F05_CIPHER_KEY_ID POSTQRON_F05_CIPHER_KEY_BASE64

cipher_key=$(value_of POSTQRON_F05_CIPHER_KEY_BASE64)
if [[ ! "$cipher_key" =~ ^[A-Za-z0-9+/]{43}=$ ]]; then
  echo "POSTQRON_F05_CIPHER_KEY_BASE64 must encode exactly 32 bytes" >&2
  exit 1
fi
if ! decoded_length=$(printf '%s' "$cipher_key" |
  openssl base64 -d -A 2>/dev/null |
  wc -c | tr -d '[:space:]'); then
  echo "POSTQRON_F05_CIPHER_KEY_BASE64 is not valid base64" >&2
  exit 1
fi
if [[ "$decoded_length" != "32" ]]; then
  echo "POSTQRON_F05_CIPHER_KEY_BASE64 must encode exactly 32 bytes" >&2
  exit 1
fi

if [[ "$first_smoke_canary" == "true" ]]; then
  printf 'F5 configuration valid for %s; catalog remains fail-closed outside the scoped canary; canonical callback: %s\n' \
    "$(IFS=,; echo "${configured_providers[*]}")" \
    "$expected_callback"
else
  printf 'F5 configuration valid for %s; canonical callback: %s\n' \
    "$(IFS=,; echo "${configured_providers[*]}")" \
    "$expected_callback"
fi
