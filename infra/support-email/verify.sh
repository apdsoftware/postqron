#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=infra/support-email/lib.sh
source "${SCRIPT_DIR}/lib.sh"

config_file=""
check_open_relay="false"

usage() {
  cat <<'EOF'
Usage: verify.sh [--config FILE] [--check-open-relay]

Checks public MX, SPF, DKIM and DMARC. If CLOUDFLARE_EMAIL_ADMIN_TOKEN is
available, it also checks routing, both verified owners, the active alias and
the sending domain. The optional relay probe is unauthenticated and must fail.
EOF
}

while (($#)); do
  case "$1" in
    --config)
      shift
      (($#)) || die "--config requires a file"
      config_file="$1"
      ;;
    --check-open-relay)
      check_open_relay="true"
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      die "unknown argument: $1"
      ;;
  esac
  shift
done

load_config "$config_file"
require_command "$DIG_BIN"

failures=0

pass() {
  printf 'PASS: %s\n' "$*"
}

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  failures=$((failures + 1))
}

mx_records=()
while IFS= read -r record; do
  [[ -n "$record" ]] && mx_records+=("$record")
done < <("$DIG_BIN" +short MX "$SUPPORT_DOMAIN")
if [[ "${#mx_records[@]}" -eq 3 ]] &&
  printf '%s\n' "${mx_records[@]}" | grep -q 'route1.mx.cloudflare.net' &&
  printf '%s\n' "${mx_records[@]}" | grep -q 'route2.mx.cloudflare.net' &&
  printf '%s\n' "${mx_records[@]}" | grep -q 'route3.mx.cloudflare.net'; then
  pass "Cloudflare Email Routing MX set is complete"
else
  fail "expected exactly the three Cloudflare Email Routing MX records"
fi

root_txt=()
while IFS= read -r record; do
  [[ -n "$record" ]] && root_txt+=("$record")
done < <("$DIG_BIN" +short TXT "$SUPPORT_DOMAIN")
root_spf=()
if ((${#root_txt[@]} > 0)); then
  while IFS= read -r record; do
    [[ -n "$record" ]] && root_spf+=("$record")
  done < <(printf '%s\n' "${root_txt[@]}" | grep -i 'v=spf1' || true)
fi
if [[ "${#root_spf[@]}" -eq 1 ]] &&
  [[ "${root_spf[0]//\"/}" == "v=spf1 include:_spf.mx.cloudflare.net ~all" ]]; then
  pass "root SPF is singular and authorizes Cloudflare"
else
  fail "root SPF must be singular and authorize only intended providers"
fi

bounce_txt=()
while IFS= read -r record; do
  [[ -n "$record" ]] && bounce_txt+=("$record")
done < <("$DIG_BIN" +short TXT "cf-bounce.${SUPPORT_DOMAIN}")
bounce_spf=()
if ((${#bounce_txt[@]} > 0)); then
  while IFS= read -r record; do
    [[ -n "$record" ]] && bounce_spf+=("$record")
  done < <(printf '%s\n' "${bounce_txt[@]}" | grep -i 'v=spf1' || true)
fi
if [[ "${#bounce_spf[@]}" -eq 1 ]] &&
  [[ "${bounce_spf[0]//\"/}" == "v=spf1 include:_spf.mx.cloudflare.net ~all" ]]; then
  pass "sending return-path SPF is present"
else
  fail "sending return-path SPF is missing or ambiguous"
fi

if [[ -n "$("$DIG_BIN" +short TXT "cf-bounce._domainkey.${SUPPORT_DOMAIN}")" ]]; then
  pass "Cloudflare sending DKIM selector is published"
else
  fail "Cloudflare sending DKIM selector is missing"
fi

dmarc_txt=()
while IFS= read -r record; do
  [[ -n "$record" ]] && dmarc_txt+=("$record")
done < <("$DIG_BIN" +short TXT "_dmarc.${SUPPORT_DOMAIN}")
dmarc_records=()
if ((${#dmarc_txt[@]} > 0)); then
  while IFS= read -r record; do
    [[ -n "$record" ]] && dmarc_records+=("$record")
  done < <(printf '%s\n' "${dmarc_txt[@]}" | grep -i 'v=DMARC1' || true)
fi
if [[ "${#dmarc_records[@]}" -eq 1 ]] &&
  [[ "${dmarc_records[0]}" == *"p=${DMARC_POLICY}"* ]] &&
  [[ "${dmarc_records[0]}" == *"adkim=s"* ]] &&
  [[ "${dmarc_records[0]}" == *"aspf=s"* ]]; then
  pass "DMARC is singular, aligned, and uses policy ${DMARC_POLICY}"
else
  fail "DMARC must be singular and match the reviewed strict-alignment policy"
fi

if [[ -n "${CLOUDFLARE_EMAIL_ADMIN_TOKEN:-}" ]]; then
  require_command "$CURL_BIN"
  require_command "$JQ_BIN"

  routing="$(cf_api GET "/zones/${CLOUDFLARE_ZONE_ID}/email/routing")"
  if "$JQ_BIN" -e '.result.status == "ready" or .result.enabled == true' \
    >/dev/null <<<"$routing"; then
    pass "Email Routing API reports ready"
  else
    fail "Email Routing API does not report ready"
  fi

  addresses="$(cf_api GET "/accounts/${CLOUDFLARE_ACCOUNT_ID}/email/routing/addresses?per_page=200")"
  for destination in "$SUPPORT_OWNER_ADDRESS" "$SUPPORT_BACKUP_OWNER_ADDRESS"; do
    if "$JQ_BIN" -e --arg destination "$destination" \
      'any(.result[]; ((.email | ascii_downcase) == ($destination | ascii_downcase)) and (.verified != null))' \
      >/dev/null <<<"$addresses"; then
      pass "verified destination exists for configured owner role"
    else
      fail "an owner destination is absent or unverified"
    fi
  done

  rules="$(cf_api GET "/zones/${CLOUDFLARE_ZONE_ID}/email/routing/rules?per_page=200")"
  if "$JQ_BIN" -e --arg support "$SUPPORT_ADDRESS" \
    'any(.result[]; .enabled and any(.matchers[]?; ((.value | ascii_downcase) == ($support | ascii_downcase))))' \
    >/dev/null <<<"$rules"; then
    pass "enabled support routing rule exists"
  else
    fail "enabled support routing rule is missing"
  fi

  sending="$(cf_api GET "/zones/${CLOUDFLARE_ZONE_ID}/email/sending/subdomains?per_page=200")"
  if "$JQ_BIN" -e --arg domain "$SUPPORT_DOMAIN" \
    'any(.result[]; ((.name | ascii_downcase) == ($domain | ascii_downcase)) and .enabled)' \
    >/dev/null <<<"$sending"; then
    pass "Email Sending API reports the domain enabled"
  else
    fail "Email Sending API does not report the domain enabled"
  fi
else
  note "SKIP: provider-state checks require CLOUDFLARE_EMAIL_ADMIN_TOKEN"
fi

if [[ "$check_open_relay" == "true" ]]; then
  require_command "$CURL_BIN"
  relay_message="$(mktemp)"
  relay_error="$(mktemp)"
  trap 'rm -f "$relay_message" "$relay_error"' EXIT
  printf 'From: probe@external.invalid\nTo: probe@other.invalid\nSubject: relay probe\n\nMust not deliver.\n' \
    >"$relay_message"
  relay_status=0
  "$CURL_BIN" \
    --silent \
    --show-error \
    --max-time 20 \
    --url "smtps://smtp.mx.cloudflare.net:465" \
    --mail-from "probe@external.invalid" \
    --mail-rcpt "probe@other.invalid" \
    --upload-file "$relay_message" >/dev/null 2>"$relay_error" || relay_status=$?
  if [[ "$relay_status" -eq 0 ]]; then
    fail "unauthenticated external-to-external SMTP relay was accepted"
  elif grep -Eq 'MAIL failed: 530|authentication required|Authentication required' "$relay_error"; then
    pass "unauthenticated external-to-external SMTP relay was rejected with authentication required"
  else
    fail "relay probe was inconclusive because SMTP was unreachable or returned an unexpected error"
  fi
fi

if ((failures > 0)); then
  die "${failures} verification check(s) failed"
fi
note "Verification complete."
