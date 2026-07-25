#!/usr/bin/env bash
set -euo pipefail

if (( $# == 0 )); then
  echo "usage: $0 <command> [args...]" >&2
  exit 2
fi

while IFS= read -r go_mod; do
  feature_dir=${go_mod%/go.mod}
  printf '\n=== %s: %s ===\n' "$feature_dir" "$*"
  (
    cd "$feature_dir"
    GOWORK=off "$@"
  )
done < <(find features -mindepth 2 -maxdepth 2 -name go.mod | sort)
