#!/usr/bin/env bash
set -euo pipefail

repository_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
temporary_directory=$(mktemp -d)
cleanup() {
  local status=$?
  if (( status != 0 )); then
    for output in "$temporary_directory"/*-output; do
      if [[ -f "$output" ]]; then
        echo "--- $(basename "$output") ---" >&2
        sed 's/^/  /' "$output" >&2
      fi
    done
  fi
  rm -rf "$temporary_directory"
  exit "$status"
}
trap cleanup EXIT

stub_directory="$temporary_directory/bin"
state_directory="$temporary_directory/state"
mkdir -p "$stub_directory" "$state_directory/variables" "$state_directory/secrets"

cat > "$stub_directory/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

kind=${1:-}
action=${2:-}
case "$kind:$action" in
  auth:status|api:*)
    exit 0
    ;;
  variable:list)
    find "$GH_STUB_STATE/variables" -type f -maxdepth 1 -exec basename {} \; | sort
    ;;
  secret:list)
    find "$GH_STUB_STATE/secrets" -type f -maxdepth 1 -exec basename {} \; | sort
    ;;
  variable:set)
    name=$3
    tee "$GH_STUB_STATE/variables/$name" >/dev/null
    ;;
  secret:set)
    name=$3
    tee "$GH_STUB_STATE/secrets/$name" >/dev/null
    ;;
  *)
    echo "unexpected gh invocation: $*" >&2
    exit 1
    ;;
esac
EOF
chmod +x "$stub_directory/gh"

touch \
  "$state_directory/variables/ADMIN_CIDRS_JSON" \
  "$state_directory/variables/DEPLOYMENT_SSH_PUBLIC_KEY" \
  "$state_directory/secrets/DEPLOYMENT_SSH_PRIVATE_KEY"

provision_output="$temporary_directory/provision-output"
printf '%s\n' \
  "postqron.example" \
  "api.postqron.example" \
  "0123456789abcdef0123456789abcdef" \
  "hcloud-test-token" \
  "cloudflare-test-token" \
  "state-access-key" \
  "state-secret-key" \
  "postqron-production-state" \
  "" \
  "" |
  GH_STUB_STATE="$state_directory" \
    PATH="$stub_directory:$PATH" \
    "$repository_root/scripts/deploy/configure-github-environment.sh" \
      --phase provision >"$provision_output" 2>&1

for variable in APP_DOMAIN API_DOMAIN CLOUDFLARE_ZONE_ID; do
  [[ -s "$state_directory/variables/$variable" ]] ||
    { echo "missing test variable $variable" >&2; exit 1; }
done
for secret in \
  HCLOUD_TOKEN \
  CLOUDFLARE_API_TOKEN \
  TF_STATE_ACCESS_KEY \
  TF_STATE_SECRET_KEY \
  TF_BACKEND_CONFIG; do
  [[ -s "$state_directory/secrets/$secret" ]] ||
    { echo "missing test secret $secret" >&2; exit 1; }
done
grep -q 'https://nbg1.your-objectstorage.com' \
  "$state_directory/secrets/TF_BACKEND_CONFIG"
if grep -Eq 'hcloud-test-token|cloudflare-test-token|state-secret-key' \
  "$provision_output"; then
  echo "provision output exposed a secret" >&2
  exit 1
fi

test_key="$temporary_directory/host_key"
ssh-keygen -q -t ed25519 -N "" -f "$test_key"
known_hosts="$temporary_directory/known_hosts"
printf '203.0.113.10 %s\n' "$(cat "$test_key.pub")" > "$known_hosts"

release_output="$temporary_directory/release-output"
printf '%s\n' \
  "$known_hosts" \
  "ghp_test_read_packages_token" \
  "operations@postqron.example" \
  "" \
  "" |
  GH_STUB_STATE="$state_directory" \
    PATH="$stub_directory:$PATH" \
    "$repository_root/scripts/deploy/configure-github-environment.sh" \
      --phase release >"$release_output" 2>&1

for secret in SSH_KNOWN_HOSTS GHCR_READ_TOKEN RUNTIME_ENV; do
  [[ -s "$state_directory/secrets/$secret" ]] ||
    { echo "missing release test secret $secret" >&2; exit 1; }
done
grep -q '^POSTGRES_PASSWORD=[a-f0-9]\{64\}$' \
  "$state_directory/secrets/RUNTIME_ENV"
grep -q '^DATABASE_URL=postgres://postqron:' \
  "$state_directory/secrets/RUNTIME_ENV"
if grep -q 'ghp_test_read_packages_token' "$release_output"; then
  echo "release output exposed a secret" >&2
  exit 1
fi

GH_STUB_STATE="$state_directory" \
  PATH="$stub_directory:$PATH" \
  "$repository_root/scripts/deploy/configure-github-environment.sh" \
    --phase provision </dev/null >/dev/null
GH_STUB_STATE="$state_directory" \
  PATH="$stub_directory:$PATH" \
  "$repository_root/scripts/deploy/configure-github-environment.sh" \
    --phase release </dev/null >/dev/null

echo "GitHub environment configuration script tests passed"
