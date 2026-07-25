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

web_fixture_source="$temporary_directory/web-fixture-source"
web_fixture_directory="$web_fixture_source/web-fixture"
web_fixture_bundle="$temporary_directory/web-fixture-bundle"
mkdir -p \
  "$web_fixture_directory/pages" \
  "$web_fixture_directory/layouts" \
  "$web_fixture_directory/components/nested" \
  "$web_fixture_directory/plugins" \
  "$web_fixture_directory/middleware"
printf 'export default {};\n' >"$web_fixture_directory/runtime.ts"
printf '<template>route</template>\n' >"$web_fixture_directory/pages/contact.vue"
printf '<template><slot /></template>\n' >"$web_fixture_directory/layouts/default.vue"
printf '<template>component</template>\n' \
  >"$web_fixture_directory/components/nested/ContactCard.vue"
printf 'export default {};\n' >"$web_fixture_directory/plugins/contact.ts"
printf 'export default {};\n' >"$web_fixture_directory/middleware/auth.ts"
cat >"$web_fixture_directory/feature.yaml" <<'YAML'
schema_version: 1
id: web-fixture
kind: web
version: 0.1.0
entrypoint: ./runtime.ts
dependencies: []
migrations: []
web:
  routes:
    - path: /contact
      file: ./pages/contact.vue
      visibility: public
      middleware: []
  layouts:
    - name: default
      file: ./layouts/default.vue
  components: [./components]
  plugins:
    - ./plugins/contact.ts
  middleware:
    - name: auth
      file: ./middleware/auth.ts
YAML

"$repository_root/scripts/runtime/bundle-features.sh" \
  "$web_fixture_source" \
  "$web_fixture_bundle"

for required_asset in \
  web-fixture/pages/contact.vue \
  web-fixture/layouts/default.vue \
  web-fixture/components/nested/ContactCard.vue \
  web-fixture/plugins/contact.ts \
  web-fixture/middleware/auth.ts; do
  if [[ ! -f "$web_fixture_bundle/$required_asset" ]]; then
    echo "feature bundle is missing declared web asset $required_asset" >&2
    exit 1
  fi
done

POSTQRON_FEATURE_ROOTS="$web_fixture_bundle" \
  go run "$repository_root/services/api/cmd/migrate" --check

assert_unsafe_web_path_rejected() {
  local description=$1
  local configured_path=$2
  local unsafe_source="$temporary_directory/unsafe-$description-source"
  local unsafe_feature="$unsafe_source/unsafe-$description"
  local unsafe_bundle="$temporary_directory/unsafe-$description-bundle"
  local error_output="$temporary_directory/unsafe-$description-error"

  mkdir -p "$unsafe_feature"
  printf 'export default {};\n' >"$unsafe_feature/runtime.ts"
  cat >"$unsafe_feature/feature.yaml" <<YAML
schema_version: 1
id: unsafe-$description
kind: web
version: 0.1.0
entrypoint: ./runtime.ts
dependencies: []
migrations: []
web:
  plugins:
    - $configured_path
YAML

  if "$repository_root/scripts/runtime/bundle-features.sh" \
    "$unsafe_source" \
    "$unsafe_bundle" 2>"$error_output"; then
    echo "feature bundle accepted unsafe $description web path" >&2
    exit 1
  fi
  if ! grep -q 'unsafe feature path:' "$error_output"; then
    echo "feature bundle did not report unsafe $description web path" >&2
    cat "$error_output" >&2
    exit 1
  fi
}

assert_unsafe_web_path_rejected traversal '../outside.ts'
assert_unsafe_web_path_rejected absolute "$temporary_directory/outside.ts"

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
