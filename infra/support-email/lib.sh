#!/usr/bin/env bash

set -euo pipefail

CURL_BIN="${CURL_BIN:-curl}"
JQ_BIN="${JQ_BIN:-jq}"
DIG_BIN="${DIG_BIN:-dig}"
CF_API_BASE="${CF_API_BASE:-https://api.cloudflare.com/client/v4}"

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

note() {
  printf '%s\n' "$*"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

valid_email() {
  [[ "$1" =~ ^[A-Za-z0-9.!#$%+\&\'*=/\?\^_\`\{\|\}~-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ ]]
}

valid_domain() {
  [[ "$1" =~ ^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$ ]]
}

load_config() {
  local config_file="${1:-}"
  local line key value

  if [[ -n "$config_file" ]]; then
    [[ -r "$config_file" ]] || die "configuration file is not readable: $config_file"
    while IFS= read -r line || [[ -n "$line" ]]; do
      line="${line%$'\r'}"
      [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
      [[ "$line" == *=* ]] || die "invalid configuration line in $config_file"
      key="${line%%=*}"
      value="${line#*=}"
      case "$key" in
        CLOUDFLARE_ACCOUNT_ID | CLOUDFLARE_ZONE_ID | SUPPORT_DOMAIN | SUPPORT_ADDRESS | \
          SUPPORT_OWNER_ADDRESS | SUPPORT_BACKUP_OWNER_ADDRESS | DMARC_REPORT_ADDRESS | \
          DMARC_POLICY)
          printf -v "$key" '%s' "$value"
          export "${key?}"
          ;;
        *)
          die "unsupported configuration key ${key}; credentials are forbidden in config files"
          ;;
      esac
    done <"$config_file"
  fi

  : "${CLOUDFLARE_ACCOUNT_ID:?CLOUDFLARE_ACCOUNT_ID is required}"
  : "${CLOUDFLARE_ZONE_ID:?CLOUDFLARE_ZONE_ID is required}"
  : "${SUPPORT_DOMAIN:?SUPPORT_DOMAIN is required}"
  : "${SUPPORT_ADDRESS:?SUPPORT_ADDRESS is required}"
  : "${SUPPORT_OWNER_ADDRESS:?SUPPORT_OWNER_ADDRESS is required}"
  : "${SUPPORT_BACKUP_OWNER_ADDRESS:?SUPPORT_BACKUP_OWNER_ADDRESS is required}"

  DMARC_REPORT_ADDRESS="${DMARC_REPORT_ADDRESS:-dmarc@${SUPPORT_DOMAIN}}"
  DMARC_POLICY="${DMARC_POLICY:-quarantine}"

  valid_domain "$SUPPORT_DOMAIN" || die "invalid SUPPORT_DOMAIN"
  valid_email "$SUPPORT_ADDRESS" || die "invalid SUPPORT_ADDRESS"
  valid_email "$SUPPORT_OWNER_ADDRESS" || die "invalid SUPPORT_OWNER_ADDRESS"
  valid_email "$SUPPORT_BACKUP_OWNER_ADDRESS" || die "invalid SUPPORT_BACKUP_OWNER_ADDRESS"
  valid_email "$DMARC_REPORT_ADDRESS" || die "invalid DMARC_REPORT_ADDRESS"
  [[ "$SUPPORT_ADDRESS" == "help@${SUPPORT_DOMAIN}" ]] ||
    die "SUPPORT_ADDRESS must be help@SUPPORT_DOMAIN"
  [[ "$SUPPORT_OWNER_ADDRESS" != "$SUPPORT_BACKUP_OWNER_ADDRESS" ]] ||
    die "owner and backup owner must use different destination addresses"
  [[ "$DMARC_POLICY" =~ ^(none|quarantine|reject)$ ]] ||
    die "DMARC_POLICY must be none, quarantine, or reject"
}

cf_api() {
  local method="$1"
  local path="$2"
  local data="${3:-}"
  local response
  local -a args

  : "${CLOUDFLARE_EMAIL_ADMIN_TOKEN:?CLOUDFLARE_EMAIL_ADMIN_TOKEN is required in apply mode}"
  args=(
    --silent
    --show-error
    --fail-with-body
    --request "$method"
    --header "Authorization: Bearer ${CLOUDFLARE_EMAIL_ADMIN_TOKEN}"
    --header "Content-Type: application/json"
    "${CF_API_BASE}${path}"
  )
  if [[ -n "$data" ]]; then
    args+=(--data "$data")
  fi

  if ! response="$("$CURL_BIN" "${args[@]}")"; then
    die "Cloudflare API request failed: ${method} ${path}"
  fi
  if ! "$JQ_BIN" -e '.success == true' >/dev/null 2>&1 <<<"$response"; then
    "$JQ_BIN" -r '.errors[]?.message // "Cloudflare API returned an unknown error"' \
      <<<"$response" >&2
    die "Cloudflare API rejected: ${method} ${path}"
  fi
  printf '%s' "$response"
}
