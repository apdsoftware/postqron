#!/usr/bin/env bash

set -euo pipefail

case "${2:-}:${3:-}" in
  MX:postqron.com)
    printf '%s\n' \
      '48 route1.mx.cloudflare.net.' \
      '85 route2.mx.cloudflare.net.' \
      '61 route3.mx.cloudflare.net.'
    ;;
  TXT:postqron.com)
    printf '"v=spf1 include:_spf.mx.cloudflare.net ~all"\n'
    ;;
  TXT:cf-bounce.postqron.com)
    printf '"v=spf1 include:_spf.mx.cloudflare.net ~all"\n'
    ;;
  TXT:cf-bounce._domainkey.postqron.com)
    printf '"v=DKIM1; p=fixture-public-key"\n'
    ;;
  TXT:_dmarc.postqron.com)
    printf '"v=DMARC1; p=quarantine; adkim=s; aspf=s; pct=100; rua=mailto:dmarc@postqron.com"\n'
    ;;
esac
