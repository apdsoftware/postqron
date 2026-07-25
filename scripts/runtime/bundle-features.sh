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

  while [[ "$configured_path" == ./* ]]; do
    configured_path=${configured_path#./}
  done
  if [[ -z "$configured_path" || "$configured_path" == /* ||
    "$configured_path" == ".." || "$configured_path" == ../* ||
    "$configured_path" == */../* || "$configured_path" == */.. ]]; then
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

copy_local_directory() {
  local feature_directory=$1
  local destination_directory=$2
  local configured_path=$3

  while [[ "$configured_path" == ./* ]]; do
    configured_path=${configured_path#./}
  done
  if [[ -z "$configured_path" || "$configured_path" == /* ||
    "$configured_path" == ".." || "$configured_path" == ../* ||
    "$configured_path" == */../* || "$configured_path" == */.. ]]; then
    echo "unsafe feature path: $configured_path" >&2
    exit 1
  fi

  local source_directory="$feature_directory/$configured_path"
  if [[ ! -d "$source_directory" || -L "$source_directory" ]]; then
    echo "feature directory does not exist: $source_directory" >&2
    exit 1
  fi
  mkdir -p "$destination_directory/$configured_path"
  cp -R "$source_directory/." "$destination_directory/$configured_path"
}

web_asset_paths() {
  local manifest_path=$1

  awk '
    function trim(value) {
      sub(/^[[:space:]]+/, "", value)
      sub(/[[:space:]]+$/, "", value)
      return value
    }
    function unquote(value, first, last) {
      value = trim(value)
      first = substr(value, 1, 1)
      last = substr(value, length(value), 1)
      if ((first == "\"" && last == "\"") ||
          (first == "\047" && last == "\047")) {
        return substr(value, 2, length(value) - 2)
      }
      return value
    }
    function emit_inline_list(kind, value, item_count, items, item_index) {
      value = trim(value)
      if (substr(value, 1, 1) != "[" ||
          substr(value, length(value), 1) != "]") {
        return
      }
      value = substr(value, 2, length(value) - 2)
      item_count = split(value, items, ",")
      for (item_index = 1; item_index <= item_count; item_index++) {
        items[item_index] = unquote(items[item_index])
        if (items[item_index] != "") {
          print kind "\t" items[item_index]
        }
      }
    }
    /^[^[:space:]#][^:]*:/ {
      in_web = ($0 ~ /^web:[[:space:]]*($|#)/)
      section = ""
      next
    }
    !in_web { next }
    /^  (routes|layouts|components|plugins|middleware):/ {
      section = $0
      sub(/^  /, "", section)
      sub(/:.*/, "", section)
      value = $0
      sub(/^  [^:]+:[[:space:]]*/, "", value)
      if (section == "components") {
        emit_inline_list("directory", value)
      } else if (section == "plugins") {
        emit_inline_list("file", value)
      }
      next
    }
    section == "components" && /^    -[[:space:]]+/ {
      value = $0
      sub(/^    -[[:space:]]+/, "", value)
      print "directory\t" unquote(value)
      next
    }
    section == "plugins" && /^    -[[:space:]]+/ {
      value = $0
      sub(/^    -[[:space:]]+/, "", value)
      print "file\t" unquote(value)
      next
    }
    (section == "routes" || section == "layouts" ||
      section == "middleware") && /^      file:[[:space:]]*/ {
      value = $0
      sub(/^      file:[[:space:]]*/, "", value)
      print "file\t" unquote(value)
    }
  ' "$manifest_path"
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

  web_asset_paths "$manifest_path" |
    while IFS=$'\t' read -r asset_type asset_path; do
      if [[ "$asset_type" == "directory" ]]; then
        copy_local_directory \
          "$feature_directory" \
          "$destination_directory" \
          "$asset_path"
      else
        copy_local_file \
          "$feature_directory" \
          "$destination_directory" \
          "$asset_path"
      fi
    done

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
