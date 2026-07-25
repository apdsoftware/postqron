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

adapter_root=$(mktemp -d)
trap 'rm -rf "$adapter_root"' EXIT
factory_inventory="$adapter_root/factories.tsv"
: >"$factory_inventory"
build_api_factories=${POSTQRON_BUILD_SERVER_FACTORIES:-}
if [[ -z "$build_api_factories" &&
  -x "$(dirname "$destination")/api" ]]; then
  build_api_factories=1
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

manifest_entrypoint_paths() {
  local manifest_path=$1

  awk '
    function value(line) {
      sub(/^[^:]+:[[:space:]]*/, "", line)
      gsub(/^["\047]|["\047]$/, "", line)
      return line
    }
    /^entrypoint:[[:space:]]*[^[:space:]#]/ {
      print value($0)
      next
    }
    /^entrypoints:[[:space:]]*($|#)/ {
      in_entrypoints = 1
      next
    }
    in_entrypoints && /^  (server|web):[[:space:]]*[^[:space:]#]/ {
      print value($0)
      next
    }
    in_entrypoints && !/^  / {
      in_entrypoints = 0
    }
  ' "$manifest_path" | LC_ALL=C sort -u
}

manifest_server_entrypoint() {
  local manifest_path=$1

  awk '
    function value(line) {
      sub(/^[^:]+:[[:space:]]*/, "", line)
      gsub(/^["\047]|["\047]$/, "", line)
      return line
    }
    /^kind:/ {
      kind = value($0)
      next
    }
    /^entrypoint:[[:space:]]*[^[:space:]#]/ {
      legacy = value($0)
      next
    }
    /^entrypoints:[[:space:]]*($|#)/ {
      in_entrypoints = 1
      next
    }
    in_entrypoints && /^  server:[[:space:]]*[^[:space:]#]/ {
      server = value($0)
      next
    }
    in_entrypoints && !/^  / {
      in_entrypoints = 0
    }
    END {
      if (server != "") {
        print server
      } else if (kind == "api") {
        print legacy
      }
    }
  ' "$manifest_path"
}

manifest_has_server_routes() {
  local manifest_path=$1

  awk '
    /^server:[[:space:]]*($|#)/ {
      in_server = 1
      next
    }
    in_server && /^  routes:[[:space:]]*($|#)/ {
      in_routes = 1
      next
    }
    in_routes && /^    -[[:space:]]+/ {
      found = 1
      exit
    }
    in_server && !/^  / {
      exit
    }
    END {
      if (!found) {
        exit 1
      }
    }
  ' "$manifest_path"
}

collect_server_factory() {
  local feature_directory=$1
  local feature_id=$2
  local server_entrypoint=$3

  if [[ -z "$server_entrypoint" ]]; then
    echo "server routes require a server entrypoint for $feature_id" >&2
    exit 1
  fi
  if [[ ! -f "$feature_directory/go.mod" ]]; then
    echo "server feature $feature_id is missing go.mod" >&2
    exit 1
  fi

  local module_path
  module_path=$(cd "$feature_directory" && GOWORK=off go list -m -f '{{.Path}}')
  if [[ ! "$module_path" =~ ^[A-Za-z0-9._~/-]+$ ]]; then
    echo "unsafe Go module path for $feature_id: $module_path" >&2
    exit 1
  fi
  printf '%s\t%s\t%s\n' \
    "$feature_id" \
    "$module_path" \
    "$feature_directory" \
    >>"$factory_inventory"
}

rebuild_api_with_factories() {
  local api_binary
  api_binary=$(cd "$(dirname "$destination")" && pwd)/api
  if [[ ! -x "$api_binary" ]]; then
    echo "API binary must exist before generating feature factories: $api_binary" >&2
    exit 1
  fi

  local generated="$adapter_root/factories_generated.go"
  {
    cat <<'EOF'
// Code generated by scripts/runtime/bundle-features.sh; DO NOT EDIT.

package main

import (
	"context"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/apdsoftware/postqron/services/api/internal/featurehost"
EOF
    local index=0
    while IFS=$'\t' read -r _ module_path _; do
      printf '\tfeature%d "%s"\n' "$index" "$module_path"
      index=$((index + 1))
    done <"$factory_inventory"
    cat <<'EOF'
)

func registerFeatureFactories(registry *featurehost.Registry) error {
EOF
    index=0
    while IFS=$'\t' read -r feature_id _ _; do
      cat <<EOF
	if err := registry.Register("$feature_id", func(
		_ context.Context,
		_ featureruntime.Feature,
		dependencies featurehost.Dependencies,
	) (featurehost.Module, error) {
		return feature$index.NewPostgresModule(
			dependencies.PostgreSQL,
			dependencies.Clock,
		)
	}); err != nil {
		return err
	}
EOF
      index=$((index + 1))
    done <"$factory_inventory"
    cat <<'EOF'
	return nil
}
EOF
  } >"$generated"
  gofmt -w "$generated"

  local workspace="$adapter_root/go.work"
  (
    cd "$adapter_root"
    GOWORK=off go work init \
      "$repository_root/packages/runtime/go" \
      "$repository_root/services/api"
  )
  while IFS=$'\t' read -r _ _ feature_directory; do
    GOWORK="$workspace" go work use "$feature_directory"
  done <"$factory_inventory"

  local generated_target="$repository_root/services/api/cmd/api/factories_generated.go"
  local overlay="$adapter_root/overlay.json"
  printf '{"Replace":{"%s":"%s"}}\n' \
    "$generated_target" \
    "$generated" \
    >"$overlay"
  (
    cd "$repository_root"
    CGO_ENABLED=0 GOWORK="$workspace" go build \
      -overlay="$overlay" \
      -trimpath \
      -ldflags="-s -w" \
      -o "$adapter_root/api" \
      ./services/api/cmd/api
  )
  mv "$adapter_root/api" "$api_binary"
}

repository_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
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

  while IFS= read -r entrypoint; do
    [[ -n "$entrypoint" ]] || continue
    copy_local_file "$feature_directory" "$destination_directory" "$entrypoint"
  done < <(manifest_entrypoint_paths "$manifest_path")

  if [[ "$build_api_factories" == "1" ]] &&
    manifest_has_server_routes "$manifest_path"; then
    feature_id=$(awk -F ':' '/^id:/ {
      value = substr($0, index($0, ":") + 1)
      gsub(/^[[:space:]"]+|[[:space:]"]+$/, "", value)
      print value
      exit
    }' "$manifest_path")
    if [[ ! "$feature_id" =~ ^[a-z][a-z0-9.-]*$ ]]; then
      echo "unsafe feature id: $feature_id" >&2
      exit 1
    fi
    server_entrypoint=$(manifest_server_entrypoint "$manifest_path")
    collect_server_factory \
      "$feature_directory" \
      "$feature_id" \
      "$server_entrypoint"
  fi

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

if [[ "$build_api_factories" == "1" && -s "$factory_inventory" ]]; then
  rebuild_api_with_factories
fi

printf 'bundled %d feature(s) into %s\n' "$manifest_count" "$destination"
