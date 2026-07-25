#!/usr/bin/env bash
set -euo pipefail

repository_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
temporary_directory=$(mktemp -d)
trap 'rm -rf "$temporary_directory"' EXIT

bundle="$temporary_directory/features"
"$repository_root/scripts/runtime/bundle-features.sh" \
  "$repository_root/features" \
  "$bundle"

assert_bundle_matches_source() {
  local description=$1
  local find_path=$2
  local source_files="$temporary_directory/source-$description"
  local bundled_files="$temporary_directory/bundled-$description"

  (
    cd "$repository_root/features"
    find . -path "$find_path" -type f | LC_ALL=C sort
  ) >"$source_files"
  (
    cd "$bundle"
    find . -path "$find_path" -type f | LC_ALL=C sort
  ) >"$bundled_files"

  if cmp -s "$source_files" "$bundled_files"; then
    return
  fi

  echo "feature bundle $description do not match source:" >&2
  comm -23 "$source_files" "$bundled_files" |
    sed 's/^/  missing: /' >&2
  comm -13 "$source_files" "$bundled_files" |
    sed 's/^/  unexpected: /' >&2
  exit 1
}

assert_bundle_matches_source manifests '*/feature.yaml'
assert_bundle_matches_source migrations '*/migrations/*.sql'

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
