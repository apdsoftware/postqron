#!/usr/bin/env bash
set -euo pipefail

repository_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
temporary_directory=$(mktemp -d)
api_pid=

cleanup() {
  if [[ -n "$api_pid" ]]; then
    kill -TERM "$api_pid" >/dev/null 2>&1 || true
    wait "$api_pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$temporary_directory"
}
trap cleanup EXIT

write_server_feature() {
  local root=$1
  local directory_name=$2
  local feature_id=$3
  local route=$4
  local entrypoint_style=$5
  local required=$6
  local feature_directory="$root/$directory_name"

  mkdir -p "$feature_directory"
  cat >"$feature_directory/go.mod" <<EOF
module example.test/$feature_id

go 1.26.0
EOF
  cat >"$feature_directory/server.go" <<EOF
package fixture

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

type Module struct {
	database *sql.DB
}

func NewPostgresModule(
	database *sql.DB,
	clock func() time.Time,
) (*Module, error) {
	if database == nil || clock == nil {
		return nil, errors.New("typed runtime dependencies are required")
	}
	return &Module{database: database}, nil
}

func (module *Module) Start(context.Context) error {
	if module.database == nil {
		return errors.New("PostgreSQL dependency is missing")
	}
	return nil
}
func (*Module) Stop(context.Context) error { return nil }
func (*Module) Ready(context.Context) error { return nil }
func (*Module) Handler(name string) (http.Handler, bool) {
	if name != "Fixture" {
		return nil, false
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = writer.Write([]byte("$feature_id:" + request.URL.Path))
	}), true
}
EOF
  {
    cat <<EOF
schema_version: 1
id: $feature_id
kind: api
version: 0.1.0
EOF
    if [[ "$entrypoint_style" == "legacy" ]]; then
      echo "entrypoint: ./server.go"
    else
      cat <<'EOF'
entrypoints:
  server: ./server.go
EOF
    fi
    cat <<EOF
dependencies: []
migrations: []
required: $required
server:
  routes:
    - path: $route
      handler: Fixture
      methods: [GET]
      visibility: public
EOF
  } >"$feature_directory/feature.yaml"
}

wait_for_api() {
  local port=$1
  local log=$2
  for _ in {1..80}; do
    if curl --fail --silent "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; then
      return
    fi
    if ! kill -0 "$api_pid" >/dev/null 2>&1; then
      cat "$log" >&2
      echo "API process exited before becoming healthy" >&2
      exit 1
    fi
    sleep 0.25
  done
  cat "$log" >&2
  echo "timed out waiting for API process" >&2
  exit 1
}

stop_api() {
  kill -TERM "$api_pid"
  wait "$api_pid"
  api_pid=
}

api_binary="$temporary_directory/api"
go build -o "$api_binary" "$repository_root/services/api/cmd/api"

source_root="$temporary_directory/source"
bundle_root="$temporary_directory/bundle"
write_server_feature "$source_root" legacy legacy-fixture /legacy legacy true
write_server_feature "$source_root" nested nested-fixture /nested nested true
POSTQRON_BUILD_SERVER_FACTORIES=1 \
  "$repository_root/scripts/runtime/bundle-features.sh" \
  "$source_root" \
  "$bundle_root"

for fixture in legacy nested; do
  test -f "$bundle_root/$fixture/server.go"
done

active_port=$((22000 + ($$ % 10000)))
active_log="$temporary_directory/active.log"
API_ADDR="127.0.0.1:$active_port" \
DATABASE_URL="postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable" \
POSTQRON_FEATURE_ROOTS="$bundle_root" \
  "$api_binary" >"$active_log" 2>&1 &
api_pid=$!
wait_for_api "$active_port" "$active_log"

ready_status=$(curl --silent --output "$temporary_directory/ready.json" \
  --write-out '%{http_code}' "http://127.0.0.1:$active_port/readyz")
if [[ "$ready_status" != "200" ]]; then
  cat "$temporary_directory/ready.json" >&2
  echo "bundled feature API readiness returned $ready_status" >&2
  exit 1
fi
for fixture in legacy nested; do
  response=$(curl --fail --silent "http://127.0.0.1:$active_port/$(
    [[ "$fixture" == "legacy" ]] && echo api/v1/legacy || echo api/v1/nested
  )")
  if [[ "$response" != "$fixture-fixture:/api/v1/$fixture" ]]; then
    echo "unexpected $fixture feature response: $response" >&2
    exit 1
  fi
done
stop_api

missing_root="$temporary_directory/missing"
write_server_feature "$missing_root" missing required-missing /missing nested true
missing_port=$((active_port + 1))
missing_log="$temporary_directory/missing.log"
API_ADDR="127.0.0.1:$missing_port" \
DATABASE_URL="postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable" \
POSTQRON_FEATURE_ROOTS="$missing_root" \
  "$api_binary" >"$missing_log" 2>&1 &
api_pid=$!
wait_for_api "$missing_port" "$missing_log"

missing_ready_status=$(curl --silent --output "$temporary_directory/missing-ready.json" \
  --write-out '%{http_code}' "http://127.0.0.1:$missing_port/readyz")
if [[ "$missing_ready_status" != "503" ]] ||
  ! grep -q 'no factory registered' "$temporary_directory/missing-ready.json"; then
  cat "$temporary_directory/missing-ready.json" >&2
  echo "required missing factory did not fail readiness" >&2
  exit 1
fi
missing_route_status=$(curl --silent --output /dev/null \
  --write-out '%{http_code}' "http://127.0.0.1:$missing_port/api/v1/missing")
if [[ "$missing_route_status" != "404" ]]; then
  echo "missing factory route returned $missing_route_status, want 404" >&2
  exit 1
fi
stop_api

echo "real API generated factory smoke tests passed"
