#!/usr/bin/env bash

set -euo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EMAIL_DIR="$(cd "${TEST_DIR}/.." && pwd)"
CONFIG="${EMAIL_DIR}/config.example.env"

output="$("${EMAIL_DIR}/provision.sh" --config "$CONFIG")"
[[ "$output" == *"Dry-run complete"* ]]
[[ "$output" == *"active destination role: owner"* ]]

backup_output="$("${EMAIL_DIR}/provision.sh" --config "$CONFIG" --target backup)"
[[ "$backup_output" == *"active destination role: backup"* ]]

verify_output="$(DIG_BIN="${TEST_DIR}/mock-dig.sh" \
  "${EMAIL_DIR}/verify.sh" --config "$CONFIG")"
[[ "$verify_output" == *"PASS: DMARC is singular"* ]]
[[ "$verify_output" == *"Verification complete"* ]]

apply_output="$(CLOUDFLARE_EMAIL_ADMIN_TOKEN=fixture-token \
  CURL_BIN="${TEST_DIR}/mock-curl.sh" \
  "${EMAIL_DIR}/provision.sh" --config "$CONFIG" --apply)"
[[ "$apply_output" == *"Provisioning complete"* ]]
[[ "$apply_output" != *"fixture-token"* ]]

if "${EMAIL_DIR}/provision.sh" --config "$CONFIG" --target invalid >/dev/null 2>&1; then
  printf 'expected invalid target to fail\n' >&2
  exit 1
fi

temporary_config="$(mktemp)"
trap 'rm -f "$temporary_config"' EXIT
sed 's/SUPPORT_BACKUP_OWNER_ADDRESS=.*/SUPPORT_BACKUP_OWNER_ADDRESS=support-owner@example.com/' \
  "$CONFIG" >"$temporary_config"
if "${EMAIL_DIR}/provision.sh" --config "$temporary_config" >/dev/null 2>&1; then
  printf 'expected duplicate owner destinations to fail\n' >&2
  exit 1
fi

printf '\nCLOUDFLARE_EMAIL_ADMIN_TOKEN=must-not-be-loaded\n' >>"$temporary_config"
if "${EMAIL_DIR}/provision.sh" --config "$temporary_config" >/dev/null 2>&1; then
  printf 'expected a credential in the config file to fail\n' >&2
  exit 1
fi

if grep -RIE \
  '(api[_-]?token|authorization: bearer|password)[[:space:]]*=[[:space:]]*[^${<[:space:]]+' \
  "${EMAIL_DIR}" --exclude='test.sh' --exclude='config.example.env' >/dev/null; then
  printf 'possible committed credential found\n' >&2
  exit 1
fi

printf 'support-email tests passed\n'
