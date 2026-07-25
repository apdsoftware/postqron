#!/usr/bin/env bash
set -euo pipefail

if (( $# != 2 )); then
  echo "usage: $0 <feature-root> <destination>" >&2
  exit 2
fi

source_root=${1%/}
destination=${2%/}
if [[ ! -d "$source_root" ]]; then
  echo "feature root does not exist: $source_root" >&2
  exit 1
fi

copy_local_file() {
  local feature_directory=$1
  local destination_directory=$2
  local configured_path=$3

  if [[ -z "$configured_path" || "$configured_path" == /* ||
    "$configured_path" == ".." || "$configured_path" == ../* ||
    "$configured_path" == */../* ]]; then
    echo "unsafe feature path: $configured_path" >&2
    exit 1
  fi

  local source_file="$feature_directory/$configured_path"
  if [[ ! -f "$source_file" ]]; then
    echo "feature file does not exist: $source_file" >&2
    exit 1
  fi
  mkdir -p "$destination_directory/$(dirname "$configured_path")"
  cp "$source_file" "$destination_directory/$configured_path"
}

mkdir -p "$destination"
manifest_count=0
while IFS= read -r -d '' manifest_path; do
  feature_directory=${manifest_path%/feature.yaml}
  relative_directory=${feature_directory#"$source_root"/}
  if [[ "$relative_directory" == "$feature_directory" ]]; then
    echo "manifest is outside feature root: $manifest_path" >&2
    exit 1
  fi

  destination_directory="$destination/$relative_directory"
  mkdir -p "$destination_directory"
  cp "$manifest_path" "$destination_directory/feature.yaml"

  entrypoint=$(awk -F ': ' '/^entrypoint:/ { print $2; exit }' "$manifest_path")
  entrypoint=${entrypoint#./}
  entrypoint=${entrypoint//\"/}
  entrypoint=${entrypoint//\'/}
  copy_local_file "$feature_directory" "$destination_directory" "$entrypoint"

  while IFS= read -r migration; do
    migration=${migration#./}
    migration=${migration//\"/}
    migration=${migration//\'/}
    copy_local_file "$feature_directory" "$destination_directory" "$migration"
  done < <(
    awk '
      /^migrations:/ { in_migrations = 1; next }
      in_migrations && /^  - / {
        value = $0
        sub(/^  - /, "", value)
        print value
        next
      }
      in_migrations { exit }
    ' "$manifest_path"
  )

  if [[ "$relative_directory" == "f23-pwa" ]]; then
    cp -R "$feature_directory/web" "$destination_directory/web"
  fi
  manifest_count=$((manifest_count + 1))
done < <(find "$source_root" -name feature.yaml -type f -print0 | sort -z)

if (( manifest_count == 0 )); then
  echo "no feature manifests found in $source_root" >&2
  exit 1
fi

printf 'bundled %d feature(s) into %s\n' "$manifest_count" "$destination"
