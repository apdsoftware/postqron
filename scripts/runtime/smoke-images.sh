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

assert_occurrences() {
  local value=$1
  local pattern=$2
  local expected=$3
  local actual
  actual=$(grep -o "$pattern" <<<"$value" | wc -l | tr -d ' ')
  if [[ "$actual" != "$expected" ]]; then
    echo "pattern $pattern occurred $actual times, want $expected" >&2
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

  local bundled_features
  bundled_features=$(docker exec "$container" \
    find /app/features -name feature.yaml -type f | wc -l | tr -d ' ')
  if [[ "$bundled_features" != "22" ]]; then
    echo "$container contains $bundled_features bundled features, want 22" >&2
    exit 1
  fi
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
}
trap cleanup EXIT

docker build -f "$repository_root/apps/web/Dockerfile" -t "$web_image" "$repository_root"
docker build -f "$repository_root/services/api/Dockerfile" -t "$api_image" "$repository_root"
docker build -f "$repository_root/services/worker/Dockerfile" -t "$worker_image" "$repository_root"

docker run -d --name "$api_container" "$api_image" >/dev/null
wait_for_http "$api_container" "http://127.0.0.1:8080/healthz"
api_catalog=$(container_fetch "$api_container" "http://127.0.0.1:8080/api/v1/features")
assert_contains "$api_catalog" '"id":"platform"'
assert_contains "$api_catalog" '"id":"integrations"'
assert_occurrences "$api_catalog" '"id":' 18
assert_clean_bundle "$api_container"
docker run --rm "$api_image" migrate --check |
  grep -q 'validated 23 feature(s) and 21 migration(s)'

docker run -d --name "$web_container" "$web_image" >/dev/null
wait_for_http "$web_container" "http://127.0.0.1:3000/api/health"
homepage=$(container_fetch "$web_container" "http://127.0.0.1:3000/")
assert_contains "$homepage" 'I tuoi contenuti social, finalmente in ordine.'
if [[ "$homepage" == *'La fondazione applicativa è pronta'* ]]; then
  echo "web image still serves the foundation placeholder" >&2
  exit 1
fi
web_catalog=$(container_fetch "$web_container" "http://127.0.0.1:3000/api/features")
assert_contains "$web_catalog" '"id":"marketing-site"'
assert_occurrences "$web_catalog" '"id":' 4
assert_contains \
  "$(container_fetch "$web_container" "http://127.0.0.1:3000/manifest.webmanifest")" \
  '"short_name": "Postqron"'
assert_contains \
  "$(container_fetch "$web_container" "http://127.0.0.1:3000/service-worker.js")" \
  'postqron-public-shell-'
container_fetch "$web_container" "http://127.0.0.1:3000/pwa/icon-192.png" >/dev/null
container_fetch "$web_container" "http://127.0.0.1:3000/pwa/pwa-client.mjs" >/dev/null
assert_web_runtime_roots "$web_container"
assert_clean_bundle "$web_container"

worker_output=$(docker run \
  --name "$worker_container" \
  -e WORKER_RUN_ONCE=1 \
  "$worker_image" 2>&1)
assert_contains "$worker_output" '"discovered_features":24'
assert_contains "$worker_output" '"features":3'
assert_contains "$worker_output" '"feature":"publishing"'
assert_contains "$worker_output" '"feature":"email"'
assert_clean_image "$worker_image"

echo "runtime image smoke tests passed"
