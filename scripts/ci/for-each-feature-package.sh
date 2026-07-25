#!/usr/bin/env bash
set -euo pipefail

if (( $# != 1 )); then
  echo "usage: $0 <package-script>" >&2
  exit 2
fi

package_script=$1
while IFS= read -r package_json; do
  if ! node -e '
    const path = require("node:path")
    const manifest = require(path.resolve(process.argv[1]))
    process.exit(Object.hasOwn(manifest.scripts || {}, process.argv[2]) ? 0 : 1)
  ' "$package_json" "$package_script"; then
    continue
  fi

  feature_dir=${package_json%/package.json}
  printf '\n=== %s: pnpm %s ===\n' "$feature_dir" "$package_script"
  pnpm --dir "$feature_dir" run "$package_script"
done < <(find features -mindepth 2 -maxdepth 2 -name package.json | sort)
