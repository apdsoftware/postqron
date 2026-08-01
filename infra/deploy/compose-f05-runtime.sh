#!/usr/bin/env bash

set -euo pipefail

if (( $# != 3 )); then
  echo "usage: compose-f05-runtime.sh RUNTIME_ENV APP_DOMAIN ENVIRONMENT" >&2
  exit 2
fi

runtime_file=$1
app_domain=$2
deployment_environment=$3
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
validator="$script_dir/validate-f05-runtime.sh"

if [[ ! -r "$runtime_file" ]]; then
  echo "legacy runtime configuration file is not readable" >&2
  exit 1
fi

# These values are supplied independently by protected GitHub production
# Environment secrets/variables. The legacy RUNTIME_ENV remains opaque and is
# copied byte-for-byte before this allowlisted inventory is appended.
dedicated_keys=(
  POSTQRON_F05_ENABLED
  POSTQRON_F05_CIPHER_KEY_ID
  POSTQRON_F05_CIPHER_KEY_BASE64
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
)

dedicated_present=false
for key in "${dedicated_keys[@]}"; do
  value=${!key-}
  if [[ -n "$value" ]]; then
    dedicated_present=true
  fi
  if [[ "$value" == *$'\n'* || "$value" == *$'\r'* ]]; then
    echo "dedicated F5 configuration contains a multiline value: $key" >&2
    exit 1
  fi
done

if [[ "$deployment_environment" == "production" ]]; then
  required_production_keys=(
    POSTQRON_F05_ENABLED
    POSTQRON_F05_CIPHER_KEY_ID
    POSTQRON_F05_CIPHER_KEY_BASE64
    POSTQRON_F05_X_ENABLED
    POSTQRON_F05_X_CLIENT_ID
    POSTQRON_F05_X_CLIENT_SECRET
    POSTQRON_F05_X_REDIRECT_URL
    POSTQRON_F05_X_API_ACCESS_APPROVED
    POSTQRON_F05_X_RUNTIME_AUDIT_VERIFIED
    POSTQRON_F05_X_SMOKE_TEST_VERIFIED
    POSTQRON_F05_X_FIRST_SMOKE_CANARY_ENABLED
  )
  for key in "${required_production_keys[@]}"; do
    if [[ -z "${!key-}" ]]; then
      echo "dedicated production F5 configuration is incomplete: $key" >&2
      exit 1
    fi
  done
  dedicated_present=true
fi

if [[ "$dedicated_present" != "true" ]]; then
  "$validator" "$runtime_file" "$app_domain" "$deployment_environment"
  exit 0
fi

for key in "${dedicated_keys[@]}"; do
  if grep --extended-regexp --quiet "^${key}=" "$runtime_file"; then
    echo "legacy RUNTIME_ENV conflicts with dedicated F5 configuration: $key" >&2
    exit 1
  fi
done

runtime_dir=$(CDPATH='' cd -- "$(dirname -- "$runtime_file")" && pwd)
runtime_name=$(basename -- "$runtime_file")
composed_file=$(mktemp "$runtime_dir/.${runtime_name}.f05.XXXXXX")
cleanup() {
  rm -f -- "$composed_file"
}
trap cleanup EXIT

install -m 0600 "$runtime_file" "$composed_file"
printf '\n' >> "$composed_file"
for key in "${dedicated_keys[@]}"; do
  printf '%s=%s\n' "$key" "${!key-}" >> "$composed_file"
done

"$validator" "$composed_file" "$app_domain" "$deployment_environment"
mv -f -- "$composed_file" "$runtime_file"
trap - EXIT

echo "dedicated F5 configuration composed without exposing values"
