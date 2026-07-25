#!/usr/bin/env bash
set -euo pipefail

repository_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
temporary_directory=$(mktemp -d)
trap 'rm -rf "$temporary_directory"' EXIT

bundle="$temporary_directory/features"
"$repository_root/scripts/runtime/bundle-features.sh" \
  "$repository_root/features" \
  "$bundle"

manifest_count=$(find "$bundle" -name feature.yaml -type f | wc -l | tr -d ' ')
migration_count=$(find "$bundle" -path '*/migrations/*.sql' -type f | wc -l | tr -d ' ')
if [[ "$manifest_count" != "22" ]]; then
  echo "feature bundle has $manifest_count manifests, want 22" >&2
  exit 1
fi
if [[ "$migration_count" != "20" ]]; then
  echo "feature bundle has $migration_count migrations, want 20" >&2
  exit 1
fi

for required_asset in \
  f23-pwa/web/manifest.webmanifest \
  f23-pwa/web/service-worker.js \
  f23-pwa/web/offline.html \
  f23-pwa/web/icon-192.png \
  f23-pwa/web/icon-512.png \
  f23-pwa/web/icon-maskable-512.png; do
  if [[ ! -f "$bundle/$required_asset" ]]; then
    echo "feature bundle is missing $required_asset" >&2
    exit 1
  fi
done

if find "$bundle" -type d \( \
  -name node_modules -o \
  -name .nuxt -o \
  -name .output -o \
  -name test \
\) -print -quit | grep -q .; then
  echo "feature bundle contains development output" >&2
  exit 1
fi

POSTQRON_FEATURE_ROOTS="$repository_root/services/api/features:$bundle" \
  go run "$repository_root/services/api/cmd/migrate" --check
