#!/usr/bin/env bash
set -euo pipefail

repository_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
smoke_suffix=$$
web_image="postqron-runtime-smoke-web:$smoke_suffix"
api_image="postqron-runtime-smoke-api:$smoke_suffix"
worker_image="postqron-runtime-smoke-worker:$smoke_suffix"
web_container="postqron-runtime-smoke-web-$smoke_suffix"
api_container="postqron-runtime-smoke-api-$smoke_suffix"
worker_container="postqron-runtime-smoke-worker-$smoke_suffix"
temporary_directory=$(mktemp -d)

container_fetch() {
  local container=$1
  local url=$2
  docker exec "$container" wget -q -O - "$url"
}

wait_for_http() {
  local container=$1
  local url=$2
  for _ in {1..30}; do
    if container_fetch "$container" "$url" >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done
  docker logs "$container" >&2
  echo "timed out waiting for $url" >&2
  exit 1
}

assert_contains() {
  local value=$1
  local expected=$2
  if [[ "$value" != *"$expected"* ]]; then
    echo "expected output to contain: $expected" >&2
    exit 1
  fi
}

assert_not_foundation_placeholder() {
  local homepage=$1
  if [[ "$homepage" == *'class="app-shell"'* ]]; then
    echo "web image still serves the foundation placeholder" >&2
    exit 1
  fi
}

assert_normal_site() {
  local homepage=$1
  assert_contains "$homepage" 'class="site-shell"'
  assert_contains "$homepage" '<main id="main-content">'
  assert_not_foundation_placeholder "$homepage"
}

assert_prelaunch_site() {
  local homepage=$1
  assert_contains "$homepage" 'data-prelaunch-mode="on"'
  assert_contains "$homepage" 'class="prelaunch-shell"'
  assert_contains "$homepage" 'id="main-content"'
  assert_not_foundation_placeholder "$homepage"
}

manifest_value() {
  local manifest=$1
  local field=$2
  awk -v field="$field" '
    index($0, field ":") == 1 {
      value = substr($0, length(field) + 2)
      sub(/^[[:space:]]+/, "", value)
      gsub(/^"|"$/, "", value)
      print value
      exit
    }
  ' "$manifest"
}

manifest_supports_host() {
  local manifest=$1
  local host=$2
  awk -v host="$host" '
    /^kind:/ {
      kind = $0
      sub(/^kind:[[:space:]]*/, "", kind)
      gsub(/^"|"$/, "", kind)
    }
    /^entrypoints:/ {
      in_entrypoints = 1
      next
    }
    in_entrypoints && /^  server:[[:space:]]*[^[:space:]]/ {
      supports_api = 1
      next
    }
    in_entrypoints && /^  web:[[:space:]]*[^[:space:]]/ {
      supports_web = 1
      next
    }
    in_entrypoints && !/^  / {
      in_entrypoints = 0
    }
    END {
      if (host == "api" && (kind == "api" || supports_api)) {
        exit 0
      }
      if (host == "web" && (kind == "web" || supports_web)) {
        exit 0
      }
      if (host == "worker" && kind == "worker") {
        exit 0
      }
      exit 1
    }
  ' "$manifest"
}

collect_feature_inventory() {
  local destination=$1
  local host=$2
  shift 2

  {
    local root
    for root in "$@"; do
      while IFS= read -r -d '' manifest; do
        if [[ -z "$host" ]] || manifest_supports_host "$manifest" "$host"; then
          manifest_value "$manifest" id
        fi
      done < <(find "$root" -name feature.yaml -type f -print0)
    done
  } | LC_ALL=C sort >"$destination"
}

collect_relative_inventory() {
  local root=$1
  local find_path=$2
  local destination=$3
  (
    cd "$root"
    find . -path "$find_path" -type f | LC_ALL=C sort
  ) >"$destination"
}

inventory_count() {
  wc -l <"$1" | tr -d ' '
}

assert_inventory_matches() {
  local description=$1
  local expected=$2
  local actual=$3
  if cmp -s "$expected" "$actual"; then
    return
  fi

  echo "$description does not match source inventory:" >&2
  comm -23 "$expected" "$actual" |
    sed 's/^/  missing: /' >&2
  comm -13 "$expected" "$actual" |
    sed 's/^/  unexpected: /' >&2
  exit 1
}

extract_json_ids() {
  local value=$1
  local field=$2
  local destination=$3
  {
    grep -o "\"$field\":\"[^\"]*\"" <<<"$value" || true
  } |
    cut -d '"' -f 4 |
    LC_ALL=C sort >"$destination"
}

assert_container_bundle_inventory() {
  local container=$1
  local actual="$temporary_directory/$container-bundle-manifests"
  docker exec "$container" sh -c \
    'cd /app/features && find . -path "*/feature.yaml" -type f | LC_ALL=C sort' \
    >"$actual"
  assert_inventory_matches \
    "$container bundled features" \
    "$shared_manifest_inventory" \
    "$actual"
}

assert_image_bundle_inventory() {
  local image=$1
  local actual="$temporary_directory/${image//:/-}-bundle-manifests"
  docker run --rm --entrypoint sh "$image" -c \
    'cd /app/features && find . -path "*/feature.yaml" -type f | LC_ALL=C sort' \
    >"$actual"
  assert_inventory_matches \
    "$image bundled features" \
    "$shared_manifest_inventory" \
    "$actual"
}

assert_exact_output() {
  local description=$1
  local actual=$2
  local expected=$3
  if [[ "$actual" != "$expected" ]]; then
    echo "$description was '$actual', want '$expected'" >&2
    exit 1
  fi
}

assert_clean_bundle() {
  local container=$1
  if docker exec "$container" sh -c \
    "find /app/features -type d \\( -name node_modules -o -name .nuxt -o -name .output -o -name test \\) -print -quit" |
    grep -q .; then
    echo "$container contains development output in /app/features" >&2
    exit 1
  fi
}

assert_clean_image() {
  local image=$1
  if docker run --rm --entrypoint sh "$image" -c \
    "find /app/features -type d \\( -name node_modules -o -name .nuxt -o -name .output -o -name test \\) -print -quit" |
    grep -q .; then
    echo "$image contains development output in /app/features" >&2
    exit 1
  fi
}

assert_web_runtime_roots() {
  local container=$1
  docker exec "$container" test -f /app/foundation/web/platform/feature.yaml
  docker exec "$container" test -f /app/foundation/api/platform/feature.yaml
  docker exec "$container" test -f /app/features/f02-marketing-site/feature.yaml
}

cleanup() {
  docker rm -f \
    "$web_container" \
    "$api_container" \
    "$worker_container" >/dev/null 2>&1 || true
  docker image rm \
    "$web_image" \
    "$api_image" \
    "$worker_image" >/dev/null 2>&1 || true
  rm -rf "$temporary_directory"
}
trap cleanup EXIT

shared_manifest_inventory="$temporary_directory/shared-manifests"
api_discovered_inventory="$temporary_directory/api-discovered"
api_host_inventory="$temporary_directory/api-host"
web_host_inventory="$temporary_directory/web-host"
worker_discovered_inventory="$temporary_directory/worker-discovered"
worker_host_inventory="$temporary_directory/worker-host"
api_migration_inventory="$temporary_directory/api-migrations"

collect_relative_inventory \
  "$repository_root/features" \
  '*/feature.yaml' \
  "$shared_manifest_inventory"
collect_feature_inventory \
  "$api_discovered_inventory" \
  "" \
  "$repository_root/services/api/features" \
  "$repository_root/features"
collect_feature_inventory \
  "$api_host_inventory" \
  api \
  "$repository_root/services/api/features" \
  "$repository_root/features"
collect_feature_inventory \
  "$web_host_inventory" \
  web \
  "$repository_root/apps/web/features" \
  "$repository_root/services/api/features" \
  "$repository_root/features"
collect_feature_inventory \
  "$worker_discovered_inventory" \
  "" \
  "$repository_root/services/worker/features" \
  "$repository_root/services/api/features" \
  "$repository_root/features"
collect_feature_inventory \
  "$worker_host_inventory" \
  worker \
  "$repository_root/services/worker/features" \
  "$repository_root/services/api/features" \
  "$repository_root/features"
collect_relative_inventory \
  "$repository_root/services/api/features" \
  '*/migrations/*.sql' \
  "$temporary_directory/api-foundation-migrations"
collect_relative_inventory \
  "$repository_root/features" \
  '*/migrations/*.sql' \
  "$temporary_directory/shared-migrations"
cat \
  "$temporary_directory/api-foundation-migrations" \
  "$temporary_directory/shared-migrations" \
  >"$api_migration_inventory"

docker build -f "$repository_root/apps/web/Dockerfile" -t "$web_image" "$repository_root"
docker build -f "$repository_root/services/api/Dockerfile" -t "$api_image" "$repository_root"
docker build -f "$repository_root/services/worker/Dockerfile" -t "$worker_image" "$repository_root"

docker run -d \
  --name "$api_container" \
  -e DATABASE_URL=postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable \
  "$api_image" >/dev/null
wait_for_http "$api_container" "http://127.0.0.1:8080/healthz"
api_catalog=$(container_fetch "$api_container" "http://127.0.0.1:8080/api/v1/features")
assert_contains "$api_catalog" '"id":"platform"'
assert_contains "$api_catalog" '"id":"integrations"'
extract_json_ids "$api_catalog" id "$temporary_directory/api-catalog"
assert_inventory_matches \
  "$api_container API features" \
  "$api_host_inventory" \
  "$temporary_directory/api-catalog"
assert_container_bundle_inventory "$api_container"
assert_clean_bundle "$api_container"
migration_output=$(docker run --rm "$api_image" migrate --check)
assert_exact_output \
  "$api_image migration validation" \
  "$migration_output" \
  "validated $(inventory_count "$api_discovered_inventory") feature(s) and $(inventory_count "$api_migration_inventory") migration(s)"

docker run -d --name "$web_container" "$web_image" >/dev/null
wait_for_http "$web_container" "http://127.0.0.1:3000/api/health"
homepage=$(container_fetch "$web_container" "http://127.0.0.1:3000/")
if [[ -f "$repository_root/features/f34-prelaunch/feature.yaml" ]]; then
  assert_prelaunch_site "$homepage"
  docker rm -f "$web_container" >/dev/null
  docker run -d \
    --name "$web_container" \
    -e PRELAUNCH_MODE=false \
    "$web_image" >/dev/null
  wait_for_http "$web_container" "http://127.0.0.1:3000/api/health"
  homepage=$(container_fetch "$web_container" "http://127.0.0.1:3000/")
  assert_contains "$homepage" 'data-prelaunch-mode="off"'
fi
assert_normal_site "$homepage"
web_catalog=$(container_fetch "$web_container" "http://127.0.0.1:3000/api/features")
assert_contains "$web_catalog" '"id":"marketing-site"'
extract_json_ids "$web_catalog" id "$temporary_directory/web-catalog"
assert_inventory_matches \
  "$web_container web features" \
  "$web_host_inventory" \
  "$temporary_directory/web-catalog"
assert_contains \
  "$(container_fetch "$web_container" "http://127.0.0.1:3000/manifest.webmanifest")" \
  '"short_name": "Postqron"'
assert_contains \
  "$(container_fetch "$web_container" "http://127.0.0.1:3000/service-worker.js")" \
  'postqron-public-shell-'
container_fetch "$web_container" "http://127.0.0.1:3000/pwa/icon-192.png" >/dev/null
container_fetch "$web_container" "http://127.0.0.1:3000/pwa/pwa-client.mjs" >/dev/null
assert_web_runtime_roots "$web_container"
assert_container_bundle_inventory "$web_container"
assert_clean_bundle "$web_container"

worker_output=$(docker run \
  --name "$worker_container" \
  -e WORKER_RUN_ONCE=1 \
  "$worker_image" 2>&1)
assert_contains \
  "$worker_output" \
  "\"discovered_features\":$(inventory_count "$worker_discovered_inventory")"
assert_contains \
  "$worker_output" \
  "\"features\":$(inventory_count "$worker_host_inventory")"
assert_contains "$worker_output" '"feature":"publishing"'
assert_contains "$worker_output" '"feature":"email"'
extract_json_ids "$worker_output" feature "$temporary_directory/worker-features"
assert_inventory_matches \
  "$worker_container worker features" \
  "$worker_host_inventory" \
  "$temporary_directory/worker-features"
assert_image_bundle_inventory "$worker_image"
assert_clean_image "$worker_image"

echo "runtime image smoke tests passed"
