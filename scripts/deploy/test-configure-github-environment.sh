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
printf '%s\n' 'opaque-runtime-value' \
  > "$state_directory/secrets/RUNTIME_ENV"

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
  "https://api.mailronix.com/email/send" \
  "help@postqron.com" \
  "true" \
  "mrx_live_test_secret_value" \
  "false" |
  GH_STUB_STATE="$state_directory" \
    PATH="$stub_directory:$PATH" \
    "$repository_root/scripts/deploy/configure-github-environment.sh" \
      --phase release >"$release_output" 2>&1

for secret in \
  SSH_KNOWN_HOSTS \
  AUTH_ENCRYPTION_KEY_B64 \
  PRIVACY_ARTIFACT_KEY_B64 \
  MAILRONIX_TRANSACTIONAL_API_KEY; do
  [[ -s "$state_directory/secrets/$secret" ]] ||
    { echo "missing release test secret $secret" >&2; exit 1; }
done
[[ "$(cat "$state_directory/variables/PRELAUNCH_MODE")" == "false" ]] ||
  { echo "PRELAUNCH_MODE was not created with the requested value" >&2; exit 1; }
[[ "$(cat "$state_directory/variables/POSTQRON_MAILRONIX_ENDPOINT")" == \
  "https://api.mailronix.com/email/send" ]] ||
  { echo "POSTQRON_MAILRONIX_ENDPOINT was not created" >&2; exit 1; }
[[ "$(cat "$state_directory/variables/POSTQRON_MAILRONIX_API_KEY_SECRET_NAME")" == \
  "MAILRONIX_TRANSACTIONAL_API_KEY" ]] ||
  { echo "POSTQRON_MAILRONIX_API_KEY_SECRET_NAME was not created" >&2; exit 1; }
[[ "$(cat "$state_directory/variables/POSTQRON_MAILRONIX_SENDER_EMAIL")" == \
  "help@postqron.com" ]] ||
  { echo "POSTQRON_MAILRONIX_SENDER_EMAIL was not created" >&2; exit 1; }
[[ "$(cat "$state_directory/variables/POSTQRON_MAILRONIX_DOMAIN_VERIFIED")" == \
  "true" ]] ||
  { echo "POSTQRON_MAILRONIX_DOMAIN_VERIFIED was not created" >&2; exit 1; }
for secret in AUTH_ENCRYPTION_KEY_B64 PRIVACY_ARTIFACT_KEY_B64; do
  [[ $(base64 --decode < "$state_directory/secrets/$secret" | wc -c) -eq 32 ]] ||
    { echo "$secret is not base64 of 32 bytes" >&2; exit 1; }
  if grep -Fq "$(cat "$state_directory/secrets/$secret")" "$release_output"; then
    echo "release output exposed $secret" >&2
    exit 1
  fi
done
if grep -Fq "$(cat "$state_directory/secrets/MAILRONIX_TRANSACTIONAL_API_KEY")" \
  "$release_output"; then
  echo "release output exposed MAILRONIX_TRANSACTIONAL_API_KEY" >&2
  exit 1
fi
if [[ "$(cat "$state_directory/secrets/RUNTIME_ENV")" != \
  "opaque-runtime-value" ]]; then
  echo "release helper unexpectedly replaced RUNTIME_ENV" >&2
  exit 1
fi
if grep -Eq 'opaque-runtime-value|POSTGRES_PASSWORD|DATABASE_URL' \
  "$release_output"; then
  echo "release output exposed a secret" >&2
  exit 1
fi

auth_key_before=$(cat "$state_directory/secrets/AUTH_ENCRYPTION_KEY_B64")
privacy_key_before=$(cat "$state_directory/secrets/PRIVACY_ARTIFACT_KEY_B64")
prelaunch_mode_before=$(cat "$state_directory/variables/PRELAUNCH_MODE")

GH_STUB_STATE="$state_directory" \
  PATH="$stub_directory:$PATH" \
  "$repository_root/scripts/deploy/configure-github-environment.sh" \
    --phase provision </dev/null >/dev/null
GH_STUB_STATE="$state_directory" \
  PATH="$stub_directory:$PATH" \
  "$repository_root/scripts/deploy/configure-github-environment.sh" \
    --phase release </dev/null >/dev/null
[[ "$(cat "$state_directory/secrets/AUTH_ENCRYPTION_KEY_B64")" == \
  "$auth_key_before" ]]
[[ "$(cat "$state_directory/secrets/PRIVACY_ARTIFACT_KEY_B64")" == \
  "$privacy_key_before" ]]
[[ "$(cat "$state_directory/variables/PRELAUNCH_MODE")" == \
  "$prelaunch_mode_before" ]]
[[ "$(cat "$state_directory/variables/POSTQRON_MAILRONIX_ENDPOINT")" == \
  "https://api.mailronix.com/email/send" ]]
[[ "$(cat "$state_directory/variables/POSTQRON_MAILRONIX_API_KEY_SECRET_NAME")" == \
  "MAILRONIX_TRANSACTIONAL_API_KEY" ]]
[[ "$(cat "$state_directory/variables/POSTQRON_MAILRONIX_SENDER_EMAIL")" == \
  "help@postqron.com" ]]
[[ "$(cat "$state_directory/variables/POSTQRON_MAILRONIX_DOMAIN_VERIFIED")" == \
  "true" ]]
[[ "$(cat "$state_directory/secrets/RUNTIME_ENV")" == \
  "opaque-runtime-value" ]]

replace_output="$temporary_directory/replace-output"
printf '%s\n' "true" |
  GH_STUB_STATE="$state_directory" \
    PATH="$stub_directory:$PATH" \
    "$repository_root/scripts/deploy/configure-github-environment.sh" \
      --phase release \
      --replace PRELAUNCH_MODE >"$replace_output" 2>&1
[[ "$(cat "$state_directory/variables/PRELAUNCH_MODE")" == "true" ]] ||
  { echo "PRELAUNCH_MODE was not replaced" >&2; exit 1; }
[[ "$(cat "$state_directory/secrets/RUNTIME_ENV")" == \
  "opaque-runtime-value" ]]
if grep -Fq 'opaque-runtime-value' "$replace_output"; then
  echo "replace output exposed RUNTIME_ENV" >&2
  exit 1
fi

for invalid_value in "TRUE" "False" " false" "false " "1"; do
  invalid_output="$temporary_directory/invalid-${invalid_value// /_}-output"
  if printf '%s\n' "$invalid_value" |
    GH_STUB_STATE="$state_directory" \
      PATH="$stub_directory:$PATH" \
      "$repository_root/scripts/deploy/configure-github-environment.sh" \
        --phase release \
        --replace PRELAUNCH_MODE >"$invalid_output" 2>&1; then
    echo "invalid PRELAUNCH_MODE was accepted" >&2
    exit 1
  fi
  grep -Fq 'PRELAUNCH_MODE must be exactly true or false' "$invalid_output"
  [[ "$(cat "$state_directory/variables/PRELAUNCH_MODE")" == "true" ]]
  [[ "$(cat "$state_directory/secrets/RUNTIME_ENV")" == \
    "opaque-runtime-value" ]]
  if grep -Fq 'opaque-runtime-value' "$invalid_output"; then
    echo "validation output exposed RUNTIME_ENV" >&2
    exit 1
  fi
done

echo "GitHub environment configuration script tests passed"
