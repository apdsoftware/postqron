#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=infra/support-email/lib.sh
source "${SCRIPT_DIR}/lib.sh"

config_file=""
recipient=""

usage() {
  cat <<'EOF'
Usage: send-test.sh [--config FILE] --to EXTERNAL_ADDRESS

Reads CLOUDFLARE_EMAIL_SEND_TOKEN from the secret store/environment and sends a
plain-text delivery probe from help@postqron.com. The token is never printed.
EOF
}

while (($#)); do
  case "$1" in
    --config)
      shift
      (($#)) || die "--config requires a file"
      config_file="$1"
      ;;
    --to)
      shift
      (($#)) || die "--to requires an address"
      recipient="$1"
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
valid_email "$recipient" || die "a valid external --to address is required"
: "${CLOUDFLARE_EMAIL_SEND_TOKEN:?set CLOUDFLARE_EMAIL_SEND_TOKEN from the secret store}"
require_command "$CURL_BIN"
require_command "$JQ_BIN"

probe_id="postqron-support-$(date -u +%Y%m%dT%H%M%SZ)"
payload="$("$JQ_BIN" -n \
  --arg from "$SUPPORT_ADDRESS" \
  --arg to "$recipient" \
  --arg reply_to "$SUPPORT_ADDRESS" \
  --arg subject "[delivery-test] ${probe_id}" \
  --arg text "Postqron support delivery probe ${probe_id}. Reply to this message to verify the inbound path and loop protection." \
  '{
    from: {address: $from, name: "Postqron Support"},
    to: [$to],
    reply_to: $reply_to,
    subject: $subject,
    text: $text,
    headers: {"X-Postqron-Delivery-Probe": $subject}
  }')"

response="$("$CURL_BIN" \
  --silent \
  --show-error \
  --fail-with-body \
  --request POST \
  --header "Authorization: Bearer ${CLOUDFLARE_EMAIL_SEND_TOKEN}" \
  --header "Content-Type: application/json" \
  --data "$payload" \
  "${CF_API_BASE}/accounts/${CLOUDFLARE_ACCOUNT_ID}/email/sending/send")" ||
  die "outbound delivery request failed"

"$JQ_BIN" -e '.success == true and (.result.permanent_bounces | length == 0)' \
  >/dev/null <<<"$response" || die "provider did not accept the outbound probe"

message_id="$("$JQ_BIN" -r '.result.message_id // "not-returned"' <<<"$response")"
note "Outbound probe accepted: ${probe_id}; message-id: ${message_id}"
note "Record delivery, Authentication-Results, reply delivery, and loop count in the private change ticket."
