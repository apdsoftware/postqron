#!/usr/bin/env bash
set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL is required}"

export F04_DATABASE_URL=$DATABASE_URL
export F05_DATABASE_URL=$DATABASE_URL
export F06_DATABASE_URL=$DATABASE_URL
export F07_DATABASE_URL=$DATABASE_URL
export F08_DATABASE_URL=$DATABASE_URL
export F09_DATABASE_URL=$DATABASE_URL
export TEST_DATABASE_URL=$DATABASE_URL
export F17_DATABASE_URL=$DATABASE_URL
export F18_DATABASE_URL=$DATABASE_URL
export F19_DATABASE_URL=$DATABASE_URL
export F20_DATABASE_URL=$DATABASE_URL
export POSTQRON_TEST_DATABASE_URL=$DATABASE_URL

failed=0
skipped=0
while IFS= read -r go_mod; do
  feature_dir=${go_mod%/go.mod}
  printf '\n=== %s: PostgreSQL integration ===\n' "$feature_dir"
  if output=$(cd "$feature_dir" && GOWORK=off go test -race -count=1 -v ./... 2>&1); then
    printf '%s\n' "$output"
  else
    printf '%s\n' "$output"
    failed=1
  fi
  if grep -q -- '--- SKIP:' <<<"$output"; then
    skipped=1
  fi
done < <(find features -mindepth 2 -maxdepth 2 -name go.mod | sort)

for worker_package in ./internal/emailruntime ./internal/privacyruntime; do
  printf '\n=== services/worker/%s: PostgreSQL integration ===\n' "$worker_package"
  if output=$(cd services/worker && GOWORK=off go test -race -count=1 -v "$worker_package" 2>&1); then
    printf '%s\n' "$output"
  else
    printf '%s\n' "$output"
    failed=1
  fi
  if grep -q -- '--- SKIP:' <<<"$output"; then
    skipped=1
  fi
done

if (( skipped != 0 )); then
  echo "At least one feature test was skipped with PostgreSQL configured." >&2
  failed=1
fi
exit "$failed"
