#!/usr/bin/env bash
# Generate a self-signed TLS certificate for local development.
#
# TLS certs are intentionally NOT committed to git or baked into the Docker
# image. Each developer / server generates its own. docker-compose mounts
# ./server/certs into the CMS container at runtime.
#
# Usage:
#   scripts/generate_certs.sh          # generate if missing
#   scripts/generate_certs.sh --force  # overwrite existing certs
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cert_dir="${repo_root}/server/certs"
crt="${cert_dir}/localhost.crt"
key="${cert_dir}/localhost.key"

force=false
if [[ "${1:-}" == "--force" ]]; then
  force=true
fi

if [[ -f "$crt" && -f "$key" && "$force" != true ]]; then
  echo "Certs already exist at ${cert_dir} (use --force to overwrite)."
  exit 0
fi

mkdir -p "$cert_dir"

openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "$key" \
  -out "$crt" \
  -days 365 \
  -subj "/CN=localhost" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"

chmod 600 "$key"
echo "Generated self-signed dev cert:"
echo "  ${crt}"
echo "  ${key}"
