#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=infra/support-email/lib.sh
source "${SCRIPT_DIR}/lib.sh"

mode="dry-run"
target="owner"
config_file=""

usage() {
  cat <<'EOF'
Usage: provision.sh [--config FILE] [--target owner|backup] [--apply]

The default is a credential-free dry-run. Apply mode reads the least-privilege
Cloudflare token from CLOUDFLARE_EMAIL_ADMIN_TOKEN and never prints it.
EOF
}

while (($#)); do
  case "$1" in
    --apply)
      mode="apply"
      ;;
    --config)
      shift
      (($#)) || die "--config requires a file"
      config_file="$1"
      ;;
    --target)
      shift
      (($#)) || die "--target requires owner or backup"
      target="$1"
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

[[ "$target" == "owner" || "$target" == "backup" ]] ||
  die "--target must be owner or backup"

load_config "$config_file"

if [[ "$target" == "owner" ]]; then
  active_destination="$SUPPORT_OWNER_ADDRESS"
else
  active_destination="$SUPPORT_BACKUP_OWNER_ADDRESS"
fi

note "Support email provisioning plan"
note "  domain: ${SUPPORT_DOMAIN}"
note "  alias: ${SUPPORT_ADDRESS}"
note "  active destination role: ${target}"
note "  DMARC policy: ${DMARC_POLICY}"
note "  actions: enable routing; verify owner destinations; configure help and DMARC routes"
note "  actions: enable sending; publish provider DNS; preserve the existing catch-all"

if [[ "$mode" == "dry-run" ]]; then
  note "Dry-run complete: no API requests or DNS changes were made."
  exit 0
fi

require_command "$CURL_BIN"
require_command "$JQ_BIN"
: "${CLOUDFLARE_EMAIL_ADMIN_TOKEN:?set CLOUDFLARE_EMAIL_ADMIN_TOKEN from the secret store}"

ensure_routing_enabled() {
  local settings status enabled
  settings="$(cf_api GET "/zones/${CLOUDFLARE_ZONE_ID}/email/routing")"
  status="$("$JQ_BIN" -r '.result.status // "unknown"' <<<"$settings")"
  enabled="$("$JQ_BIN" -r '.result.enabled // false' <<<"$settings")"
  if [[ "$status" == "ready" || "$status" == "enabled" || "$enabled" == "true" ]]; then
    note "Email Routing is already enabled."
    return
  fi
  cf_api POST "/zones/${CLOUDFLARE_ZONE_ID}/email/routing/dns" '{}' >/dev/null
  note "Enabled Email Routing and its provider-managed DNS records."
}

ensure_destination() {
  local address="$1"
  local role="$2"
  local addresses entry verified
  addresses="$(cf_api GET "/accounts/${CLOUDFLARE_ACCOUNT_ID}/email/routing/addresses?per_page=200")"
  entry="$("$JQ_BIN" -c --arg address "$address" \
    '[.result[] | select((.email | ascii_downcase) == ($address | ascii_downcase))][0] // empty' \
    <<<"$addresses")"
  if [[ -z "$entry" ]]; then
    cf_api POST "/accounts/${CLOUDFLARE_ACCOUNT_ID}/email/routing/addresses" \
      "$("$JQ_BIN" -n --arg email "$address" '{email: $email}')" >/dev/null
    note "Created ${role} destination; that owner must complete Cloudflare verification."
    destination_status="pending"
    return
  fi
  verified="$("$JQ_BIN" -r '.verified // "pending"' <<<"$entry")"
  if [[ "$verified" == "pending" ]]; then
    note "The ${role} destination is awaiting verification."
    destination_status="pending"
    return
  fi
  note "The ${role} destination is verified."
  destination_status="verified"
}

ensure_route() {
  local source_address="$1"
  local destination="$2"
  local route_name="$3"
  local rules rule_id payload method path
  rules="$(cf_api GET "/zones/${CLOUDFLARE_ZONE_ID}/email/routing/rules?per_page=200")"
  rule_id="$("$JQ_BIN" -r --arg source "$source_address" \
    '[.result[] | select(any(.matchers[]?; (.field == "to") and ((.value | ascii_downcase) == ($source | ascii_downcase))))][0].id // empty' \
    <<<"$rules")"
  payload="$("$JQ_BIN" -n \
    --arg source "$source_address" \
    --arg destination "$destination" \
    --arg name "$route_name" \
    '{
      actions: [{type: "forward", value: [$destination]}],
      matchers: [{type: "literal", field: "to", value: $source}],
      enabled: true,
      name: $name,
      source: "api"
    }')"
  if [[ -n "$rule_id" ]]; then
    method="PUT"
    path="/zones/${CLOUDFLARE_ZONE_ID}/email/routing/rules/${rule_id}"
  else
    method="POST"
    path="/zones/${CLOUDFLARE_ZONE_ID}/email/routing/rules"
  fi
  cf_api "$method" "$path" "$payload" >/dev/null
  note "Configured ${source_address} -> active ${target} destination."
}

ensure_sending_domain() {
  local domains subdomain_id
  domains="$(cf_api GET "/zones/${CLOUDFLARE_ZONE_ID}/email/sending/subdomains?per_page=200")"
  subdomain_id="$("$JQ_BIN" -r --arg domain "$SUPPORT_DOMAIN" \
    '[.result[] | select((.name | ascii_downcase) == ($domain | ascii_downcase))][0].tag // empty' \
    <<<"$domains")"
  if [[ -z "$subdomain_id" ]]; then
    domains="$(cf_api POST "/zones/${CLOUDFLARE_ZONE_ID}/email/sending/subdomains" \
      "$("$JQ_BIN" -n --arg name "$SUPPORT_DOMAIN" '{name: $name}')")"
    subdomain_id="$("$JQ_BIN" -r '.result.tag' <<<"$domains")"
    note "Enabled Email Sending for ${SUPPORT_DOMAIN}."
  else
    note "Email Sending is already configured for ${SUPPORT_DOMAIN}."
  fi

  cf_api GET \
    "/zones/${CLOUDFLARE_ZONE_ID}/email/sending/subdomains/${subdomain_id}/dns" \
    >/dev/null
  note "Confirmed provider-managed sending DNS is available."
}

ensure_dmarc() {
  local name="_dmarc.${SUPPORT_DOMAIN}"
  local content="v=DMARC1; p=${DMARC_POLICY}; adkim=s; aspf=s; pct=100; rua=mailto:${DMARC_REPORT_ADDRESS}"
  local records count record_id payload
  records="$(cf_api GET "/zones/${CLOUDFLARE_ZONE_ID}/dns_records?type=TXT&name=${name}&per_page=100")"
  count="$("$JQ_BIN" -r '.result | length' <<<"$records")"
  [[ "$count" -le 1 ]] || die "multiple DMARC records exist for ${name}; consolidate them manually"
  payload="$("$JQ_BIN" -n --arg name "$name" --arg content "$content" \
    '{type: "TXT", name: $name, content: $content, ttl: 3600, proxied: false}')"
  if [[ "$count" -eq 0 ]]; then
    cf_api POST "/zones/${CLOUDFLARE_ZONE_ID}/dns_records" "$payload" >/dev/null
    note "Created DMARC policy ${DMARC_POLICY}."
    return
  fi
  record_id="$("$JQ_BIN" -r '.result[0].id' <<<"$records")"
  if [[ "$("$JQ_BIN" -r '.result[0].content' <<<"$records")" == "$content" ]]; then
    note "DMARC policy already matches the requested configuration."
    return
  fi
  cf_api PUT "/zones/${CLOUDFLARE_ZONE_ID}/dns_records/${record_id}" "$payload" >/dev/null
  note "Updated the single DMARC policy to ${DMARC_POLICY}."
}

ensure_routing_enabled
destination_status=""
ensure_destination "$SUPPORT_OWNER_ADDRESS" "owner"
owner_status="$destination_status"
ensure_destination "$SUPPORT_BACKUP_OWNER_ADDRESS" "backup owner"
backup_status="$destination_status"
ensure_sending_domain
ensure_dmarc

if [[ "$target" == "owner" ]]; then
  target_status="$owner_status"
else
  target_status="$backup_status"
fi

if [[ "$target_status" != "verified" ]]; then
  note "Provisioning paused safely: verify both destination mailboxes, then rerun --apply."
  exit 3
fi

ensure_route "$SUPPORT_ADDRESS" "$active_destination" "Postqron operational support"
ensure_route "$DMARC_REPORT_ADDRESS" "$active_destination" "Postqron DMARC reports"

if [[ "$owner_status" != "verified" || "$backup_status" != "verified" ]]; then
  note "Provisioning applied, but both owners must be verified before production sign-off."
  exit 3
fi

note "Provisioning complete. Run verify.sh and the external delivery test matrix."
