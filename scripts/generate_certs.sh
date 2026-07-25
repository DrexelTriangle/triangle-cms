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

# World-readable: this is a disposable, non-committed, self-signed localhost dev
# key. The CMS container runs as a non-root user (UID 10001, see server/Dockerfile)
# and reads this file over a bind mount, where its host owner UID won't match, so
# 0600 would deny access and break TLS startup. Production keys are provisioned by
# ops with ownership/perms scoped to the runtime user (or TLS is terminated at Nginx).
chmod 644 "$key"
echo "Generated self-signed dev cert:"
echo "  ${crt}"
echo "  ${key}"
