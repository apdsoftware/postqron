#!/usr/bin/env bash
set -euo pipefail

repository="apdsoftware/postqron"
environment="production"
phase="provision"
replace_names=""

usage() {
  cat <<'EOF'
Usage:
  configure-github-environment.sh [options]

Options:
  --repo OWNER/REPO       GitHub repository (default: apdsoftware/postqron)
  --environment NAME      GitHub environment (default: production)
  --phase PHASE           provision or release (default: provision)
  --replace NAME          Replace one existing variable or secret; repeatable
  --help                  Show this help

The provision phase configures public domains, Cloudflare, Hetzner, Terraform
state, the administrative CIDR allowlist, and the deployment SSH key.

The release phase configures the verified SSH host key and generates dedicated
auth and privacy encryption keys when they are missing. It configures
PRELAUNCH_MODE with a fail-closed default of true and accepts only exact true or
false values. It never reads or replaces RUNTIME_ENV. GHCR uses the workflow's
temporary token.
EOF
}

fail() {
  echo "$*" >&2
  exit 1
}

while (( $# > 0 )); do
  case "$1" in
    --repo)
      (( $# >= 2 )) || fail "--repo requires a value"
      repository=$2
      shift 2
      ;;
    --environment)
      (( $# >= 2 )) || fail "--environment requires a value"
      environment=$2
      shift 2
      ;;
    --phase)
      (( $# >= 2 )) || fail "--phase requires a value"
      phase=$2
      shift 2
      ;;
    --replace)
      (( $# >= 2 )) || fail "--replace requires a name"
      replace_names="${replace_names}${2}"$'\n'
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      fail "unknown option: $1"
      ;;
  esac
done

[[ "$repository" =~ ^[^/[:space:]]+/[^/[:space:]]+$ ]] ||
  fail "repository must use OWNER/REPO format"
[[ "$environment" =~ ^[A-Za-z0-9_-]+$ ]] ||
  fail "environment contains unsupported characters"
[[ "$phase" == "provision" || "$phase" == "release" ]] ||
  fail "phase must be provision or release"

for command_name in gh jq; do
  command -v "$command_name" >/dev/null ||
    fail "required command is unavailable: $command_name"
done

if [[ "$phase" == "provision" ]]; then
  for command_name in curl ssh-keygen; do
    command -v "$command_name" >/dev/null ||
      fail "required command is unavailable: $command_name"
  done
else
  for command_name in openssl ssh-keygen; do
    command -v "$command_name" >/dev/null ||
      fail "required command is unavailable: $command_name"
  done
fi

gh auth status >/dev/null
gh api "repos/$repository/environments/$environment" >/dev/null ||
  fail "GitHub environment does not exist: $repository/$environment"

existing_variables=$(gh variable list \
  --env "$environment" \
  --repo "$repository" \
  --json name \
  --jq '.[].name')
existing_secrets=$(gh secret list \
  --env "$environment" \
  --repo "$repository" \
  --json name \
  --jq '.[].name')

contains_name() {
  local names=$1
  local expected=$2
  grep -Fqx "$expected" <<<"$names"
}

replace_requested() {
  local expected=$1
  contains_name "$replace_names" "$expected"
}

needs_variable() {
  local name=$1
  if contains_name "$existing_variables" "$name" &&
    ! replace_requested "$name"; then
    echo "skip variable $name (already configured)"
    return 1
  fi
}

needs_secret() {
  local name=$1
  if contains_name "$existing_secrets" "$name" &&
    ! replace_requested "$name"; then
    echo "skip secret $name (already configured)"
    return 1
  fi
}

prompt_value() {
  local label=$1
  local default_value=${2:-}
  local value
  if [[ -n "$default_value" ]]; then
    printf '%s [%s]: ' "$label" "$default_value" >&2
  else
    printf '%s: ' "$label" >&2
  fi
  IFS= read -r value || fail "input ended while reading $label"
  value=${value:-$default_value}
  [[ -n "$value" ]] || fail "$label cannot be empty"
  printf '%s' "$value"
}

set_variable() {
  local name=$1
  local value=$2
  printf '%s' "$value" |
    gh variable set "$name" --env "$environment" --repo "$repository"
  existing_variables="${existing_variables}${existing_variables:+$'\n'}${name}"
  echo "configured variable $name"
}

set_prompted_secret() {
  local name=$1
  local label=$2
  local value
  printf '%s: ' "$label" >&2
  IFS= read -r -s value || fail "input ended while reading $label"
  printf '\n' >&2
  [[ -n "$value" ]] || fail "$label cannot be empty"
  printf '%s' "$value" |
    gh secret set "$name" --env "$environment" --repo "$repository"
  value=""
  existing_secrets="${existing_secrets}${existing_secrets:+$'\n'}${name}"
  echo "configured secret $name"
}

is_fqdn() {
  local domain=$1
  local label
  local labels
  (( ${#domain} <= 253 )) || return 1
  [[ "$domain" != *..* && "$domain" != .* && "$domain" != *. ]] || return 1
  IFS=. read -r -a labels <<<"$domain"
  (( ${#labels[@]} >= 2 )) || return 1
  for label in "${labels[@]}"; do
    [[ "$label" =~ ^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$ ]] ||
      return 1
  done
  [[ "${labels[${#labels[@]} - 1]}" =~ ^[A-Za-z]{2,63}$ ]]
}

configure_domain() {
  local name=$1
  local label=$2
  local value
  needs_variable "$name" || return 0
  value=$(prompt_value "$label")
  is_fqdn "$value" ||
    fail "$name must be a fully qualified domain name"
  set_variable "$name" "$value"
}

configure_zone_id() {
  local value
  needs_variable "CLOUDFLARE_ZONE_ID" || return 0
  value=$(prompt_value "Cloudflare Zone ID")
  [[ "$value" =~ ^[A-Fa-f0-9]{32}$ ]] ||
    fail "Cloudflare Zone ID must contain 32 hexadecimal characters"
  set_variable "CLOUDFLARE_ZONE_ID" "$value"
}

configure_admin_cidrs() {
  local address
  local cidr
  local current_ipv4
  local default_cidrs
  local octet
  local prefix
  local value
  needs_variable "ADMIN_CIDRS_JSON" || return 0
  current_ipv4=$(curl --fail --silent --show-error --retry 3 \
    https://checkip.amazonaws.com)
  [[ "$current_ipv4" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] ||
    fail "could not determine a valid current public IPv4"
  default_cidrs="[\"$current_ipv4/32\"]"
  value=$(prompt_value "Administrative CIDRs as a JSON array" "$default_cidrs")
  jq --exit-status \
    'type == "array" and length > 0 and all(.[]; type == "string")' \
    <<<"$value" >/dev/null ||
    fail "ADMIN_CIDRS_JSON must be a non-empty JSON string array"
  while IFS= read -r cidr; do
    [[ "$cidr" == */* && "$cidr" != *[[:space:]]* ]] ||
      fail "invalid administrative CIDR: $cidr"
    address=${cidr%/*}
    prefix=${cidr##*/}
    [[ "$prefix" =~ ^[0-9]+$ ]] ||
      fail "invalid administrative CIDR prefix: $cidr"
    if [[ "$address" == *:* ]]; then
      if [[ ! "$address" =~ ^[A-Fa-f0-9:]+$ ]] ||
        (( 10#$prefix > 128 )); then
        fail "invalid IPv6 administrative CIDR: $cidr"
      fi
    else
      if [[ ! "$address" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] ||
        (( 10#$prefix > 32 )); then
        fail "invalid IPv4 administrative CIDR: $cidr"
      fi
      IFS=. read -r -a octets <<<"$address"
      for octet in "${octets[@]}"; do
        (( 10#$octet <= 255 )) ||
          fail "invalid IPv4 administrative CIDR: $cidr"
      done
    fi
  done < <(jq --raw-output '.[]' <<<"$value")
  value=$(jq --compact-output . <<<"$value")
  set_variable "ADMIN_CIDRS_JSON" "$value"
}

configure_deployment_key() {
  local configure_public=0
  local configure_private=0
  local private_key_path
  local public_key

  if replace_requested "DEPLOYMENT_SSH_PUBLIC_KEY" ||
    replace_requested "DEPLOYMENT_SSH_PRIVATE_KEY"; then
    configure_public=1
    configure_private=1
  else
    needs_variable "DEPLOYMENT_SSH_PUBLIC_KEY" && configure_public=1
    needs_secret "DEPLOYMENT_SSH_PRIVATE_KEY" && configure_private=1
  fi
  if (( configure_public == 0 && configure_private == 0 )); then
    return
  fi

  private_key_path=$(prompt_value \
    "Absolute path to the unencrypted Ed25519 deployment private key")
  [[ "$private_key_path" == /* && -f "$private_key_path" ]] ||
    fail "deployment private key path must be an existing absolute file"
  public_key=$(ssh-keygen -y -P "" -f "$private_key_path" 2>/dev/null) ||
    fail "deployment key must be a valid unencrypted private key"
  [[ "$public_key" == ssh-ed25519\ * ]] ||
    fail "deployment key must use Ed25519"

  if (( configure_private == 1 )); then
    gh secret set "DEPLOYMENT_SSH_PRIVATE_KEY" \
      --env "$environment" \
      --repo "$repository" < "$private_key_path"
    existing_secrets="${existing_secrets}${existing_secrets:+$'\n'}DEPLOYMENT_SSH_PRIVATE_KEY"
    echo "configured secret DEPLOYMENT_SSH_PRIVATE_KEY"
  fi
  if (( configure_public == 1 )); then
    set_variable "DEPLOYMENT_SSH_PUBLIC_KEY" \
      "$public_key postqron-$environment"
  fi
}

configure_backend() {
  local bucket
  local region
  local state_key
  needs_secret "TF_BACKEND_CONFIG" || return 0

  bucket=$(prompt_value \
    "Terraform state bucket" \
    "postqron-terraform-state-$environment")
  [[ "$bucket" =~ ^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$ ]] ||
    fail "state bucket must be a valid S3 bucket name"
  region=$(prompt_value "Object Storage region" "nbg1")
  [[ "$region" =~ ^[a-z0-9-]+$ ]] ||
    fail "Object Storage region contains unsupported characters"
  state_key=$(prompt_value \
    "Terraform state object key" \
    "postqron/$environment/terraform.tfstate")
  [[ "$state_key" =~ ^[A-Za-z0-9._/-]+$ ]] ||
    fail "Terraform state key contains unsupported characters"

  {
    printf 'bucket = "%s"\n' "$bucket"
    printf 'key = "%s"\n' "$state_key"
    printf 'region = "%s"\n' "$region"
    printf 'use_path_style = true\n'
    printf 'skip_credentials_validation = true\n'
    printf 'skip_region_validation = true\n'
    printf 'skip_requesting_account_id = true\n'
    printf 'skip_metadata_api_check = true\n'
    printf 'skip_s3_checksum = true\n'
    printf 'endpoints = {\n'
    printf '  s3 = "https://%s.your-objectstorage.com"\n' "$region"
    printf '}\n'
  } | gh secret set "TF_BACKEND_CONFIG" \
    --env "$environment" \
    --repo "$repository"
  existing_secrets="${existing_secrets}${existing_secrets:+$'\n'}TF_BACKEND_CONFIG"
  echo "configured secret TF_BACKEND_CONFIG"
}

configure_known_hosts() {
  local key_count
  local known_hosts_path
  needs_secret "SSH_KNOWN_HOSTS" || return 0
  known_hosts_path=$(prompt_value \
    "Absolute path to the verified SSH known_hosts file")
  [[ "$known_hosts_path" == /* && -f "$known_hosts_path" ]] ||
    fail "known_hosts path must be an existing absolute file"
  key_count=$(grep -Evc '^[[:space:]]*(#|$)' "$known_hosts_path")
  (( key_count == 1 )) ||
    fail "known_hosts file must contain exactly one verified host key"
  grep -Eq '^[^[:space:]]+[[:space:]]+ssh-ed25519[[:space:]]' \
    "$known_hosts_path" ||
    fail "known_hosts file must contain one Ed25519 host key"
  ssh-keygen -lf "$known_hosts_path" >/dev/null ||
    fail "known_hosts file is invalid"
  gh secret set "SSH_KNOWN_HOSTS" \
    --env "$environment" \
    --repo "$repository" < "$known_hosts_path"
  existing_secrets="${existing_secrets}${existing_secrets:+$'\n'}SSH_KNOWN_HOSTS"
  echo "configured secret SSH_KNOWN_HOSTS"
}

configure_generated_encryption_key() {
  local name=$1
  local value
  if contains_name "$existing_secrets" "$name"; then
    echo "skip secret $name (already configured)"
    return
  fi
  if replace_requested "$name"; then
    fail "$name cannot be replaced by this helper"
  fi
  value=$(openssl rand -base64 32)
  [[ "$value" =~ ^[A-Za-z0-9+/]{43}=$ ]] ||
    fail "could not generate $name"
  printf '%s' "$value" |
    gh secret set "$name" --env "$environment" --repo "$repository"
  value=""
  existing_secrets="${existing_secrets}${existing_secrets:+$'\n'}${name}"
  echo "configured secret $name"
}

configure_prelaunch_mode() {
  local value
  needs_variable "PRELAUNCH_MODE" || return 0
  value=$(prompt_value "Pre-launch mode (true or false)" "true")
  [[ "$value" == "true" || "$value" == "false" ]] ||
    fail "PRELAUNCH_MODE must be exactly true or false"
  set_variable "PRELAUNCH_MODE" "$value"
}

if [[ "$phase" == "provision" ]]; then
  configure_domain "APP_DOMAIN" "Application domain"
  configure_domain "API_DOMAIN" "API domain"
  configure_zone_id
  configure_admin_cidrs
  configure_deployment_key
  needs_secret "HCLOUD_TOKEN" &&
    set_prompted_secret "HCLOUD_TOKEN" "Hetzner Cloud read/write token"
  needs_secret "CLOUDFLARE_API_TOKEN" &&
    set_prompted_secret "CLOUDFLARE_API_TOKEN" "Cloudflare zone-scoped DNS token"
  needs_secret "TF_STATE_ACCESS_KEY" &&
    set_prompted_secret "TF_STATE_ACCESS_KEY" "Object Storage access key"
  needs_secret "TF_STATE_SECRET_KEY" &&
    set_prompted_secret "TF_STATE_SECRET_KEY" "Object Storage secret key"
  configure_backend
else
  configure_known_hosts
  configure_generated_encryption_key "AUTH_ENCRYPTION_KEY_B64"
  configure_generated_encryption_key "PRIVACY_ARTIFACT_KEY_B64"
  configure_prelaunch_mode
fi

echo
echo "GitHub $environment $phase configuration completed for $repository."
