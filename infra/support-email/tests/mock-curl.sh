#!/usr/bin/env bash

set -euo pipefail

method=""
url=""
while (($#)); do
  case "$1" in
    --request)
      shift
      method="${1:-}"
      ;;
    http://* | https://*)
      url="$1"
      ;;
  esac
  shift
done

case "${method}:${url}" in
  GET:*/email/routing)
    printf '{"success":true,"result":{"enabled":true,"status":"ready"}}'
    ;;
  GET:*/email/routing/addresses?*)
    printf '%s' '{"success":true,"result":['
    printf '%s' '{"email":"support-owner@example.com","verified":"2026-07-24T00:00:00Z"},'
    printf '%s' '{"email":"support-backup@example.com","verified":"2026-07-24T00:00:00Z"}'
    printf '%s' ']}'
    ;;
  GET:*/email/sending/subdomains/fixture-tag/dns)
    printf '{"success":true,"result":[{"type":"TXT","name":"cf-bounce.postqron.com","content":"fixture"}]}'
    ;;
  GET:*/email/sending/subdomains\?*)
    printf '{"success":true,"result":[{"name":"postqron.com","tag":"fixture-tag","enabled":true}]}'
    ;;
  GET:*/dns_records?*)
    printf '%s' '{"success":true,"result":[{"id":"dmarc-fixture","content":'
    printf '%s' '"v=DMARC1; p=quarantine; adkim=s; aspf=s; pct=100; rua=mailto:dmarc@postqron.com"'
    printf '%s' '}]}'
    ;;
  GET:*/email/routing/rules?*)
    printf '{"success":true,"result":[]}'
    ;;
  POST:*/email/routing/rules)
    printf '{"success":true,"result":{"id":"rule-fixture"}}'
    ;;
  *)
    printf 'unexpected mock request: %s %s\n' "$method" "$url" >&2
    exit 22
    ;;
esac
