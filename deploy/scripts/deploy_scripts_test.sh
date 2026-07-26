#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

TEST_ROOT="$(mktemp -d)"
cleanup_all() {
  rm -rf "${TEST_ROOT}"
}
trap cleanup_all EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_file_contains() {
  local path="$1"
  local pattern="$2"
  [[ -f "${path}" ]] || fail "expected file to exist: ${path}"
  grep -q "${pattern}" "${path}" || fail "expected ${path} to contain ${pattern}"
}

assert_file_not_contains() {
  local path="$1"
  local pattern="$2"
  [[ -f "${path}" ]] || fail "expected file to exist: ${path}"
  if grep -q "${pattern}" "${path}"; then
    fail "expected ${path} not to contain ${pattern}"
  fi
}

assert_no_switch_temps() {
  local dir="$1"
  if find "${dir}" -maxdepth 1 -type d -name 'triangle-cms-switch.*' | grep -q .; then
    fail "switch temp directory was not cleaned up in ${dir}"
  fi
}

assert_no_docker_invocations() {
  [[ ! -s "${FAKE_DOCKER_LOG}" ]] || fail "expected no docker invocations"
}

make_case() {
  local name="$1"
  local dir="${TEST_ROOT}/${name}"
  mkdir -p "${dir}/bin" "${dir}/triangle-cms"
  NGINX_ACTIVE_INCLUDE="${dir}/triangle-cms/active-upstreams.conf"
  ENV_FILE="${dir}/cms.env"
  COMPOSE_FILE="${dir}/compose.yml"
  DEPLOY_LOCK_FILE="${dir}/deploy.lock"
  PUBLIC_BASE_URL="http://public.test"
  BACKEND_HEALTH_TIMEOUT=0
  FRONTEND_HEALTH_TIMEOUT=0
  PUBLIC_HEALTH_TIMEOUT=0
  DEPLOY_TEST_MODE=1
  NGINX_TEST_CMD='exit "${FAKE_NGINX_TEST_STATUS:-0}"'
  NGINX_RELOAD_CMD='exit "${FAKE_NGINX_RELOAD_STATUS:-0}"'
  NGINX_RELOAD_CHECK_CMD='exit "${FAKE_NGINX_RELOAD_CHECK_STATUS:-0}"'
  FAKE_NGINX_TEST_STATUS=0
  FAKE_NGINX_RELOAD_STATUS=0
  FAKE_NGINX_RELOAD_CHECK_STATUS=0
  FAIL_READINESS=0
  FAIL_PUBLIC=0
  export NGINX_ACTIVE_INCLUDE ENV_FILE COMPOSE_FILE DEPLOY_LOCK_FILE PUBLIC_BASE_URL
  export BACKEND_HEALTH_TIMEOUT FRONTEND_HEALTH_TIMEOUT PUBLIC_HEALTH_TIMEOUT
  export DEPLOY_TEST_MODE NGINX_TEST_CMD NGINX_RELOAD_CMD NGINX_RELOAD_CHECK_CMD
  export FAKE_NGINX_TEST_STATUS FAKE_NGINX_RELOAD_STATUS FAKE_NGINX_RELOAD_CHECK_STATUS
  export FAIL_READINESS FAIL_PUBLIC
  : > "${ENV_FILE}"
  : > "${COMPOSE_FILE}"
  CASE_DIR="${dir}"
}

write_fake_bin() {
  local dir="$1"
  cat > "${dir}/bin/docker" <<'EOF'
#!/usr/bin/env bash
echo "docker $*" >> "${FAKE_DOCKER_LOG}"
exit 0
EOF
  cat > "${dir}/bin/curl" <<'EOF'
#!/usr/bin/env bash
url="${@: -1}"
echo "curl ${url}" >> "${FAKE_CURL_LOG}"
if [[ "${FAIL_READINESS:-0}" == "1" && "${url}" == *":8082/v1/health/db" ]]; then
  exit 1
fi
if [[ "${FAIL_PUBLIC:-0}" == "1" && "${url}" == "${PUBLIC_BASE_URL}"* ]]; then
  exit 1
fi
exit 0
EOF
  chmod +x "${dir}/bin/docker" "${dir}/bin/curl"
  FAKE_DOCKER_LOG="${dir}/docker.log"
  FAKE_CURL_LOG="${dir}/curl.log"
  export FAKE_DOCKER_LOG FAKE_CURL_LOG
  PATH="${dir}/bin:${PATH}"
  export PATH
}

run_deploy_expect_failure_without_docker() {
  local sha
  sha="0123456789abcdef0123456789abcdef01234567"
  write_fake_bin "${CASE_DIR}"
  if "${SCRIPT_DIR}/deploy.sh" "${sha}"; then
    fail "expected deploy preflight failure"
  fi
  assert_no_docker_invocations
}

test_first_deployment_switch() {
  local dir
  make_case first
  dir="${CASE_DIR}"
  transactional_switch_slot green
  assert_file_contains "${NGINX_ACTIVE_INCLUDE}" 'triangle_cms_slot green'
  assert_no_switch_temps "${dir}"
}

test_blue_to_green_switch() {
  local dir
  make_case blue_green
  dir="${CASE_DIR}"
  write_include_file blue "${NGINX_ACTIVE_INCLUDE}"
  transactional_switch_slot green
  assert_file_contains "${NGINX_ACTIVE_INCLUDE}" 'triangle_cms_slot green'
  assert_no_switch_temps "${dir}"
}

test_malformed_active_include_fails() {
  local dir
  make_case malformed
  dir="${CASE_DIR}"
  echo 'set $triangle_cms_slot purple;' > "${NGINX_ACTIVE_INCLUDE}"
  if active_slot >/dev/null; then
    fail "expected malformed active include to fail"
  fi
  assert_no_switch_temps "${dir}"
}

test_nginx_test_failure_restores_include() {
  local dir
  make_case test_fail
  dir="${CASE_DIR}"
  write_include_file blue "${NGINX_ACTIVE_INCLUDE}"
  FAKE_NGINX_TEST_STATUS=1
  export FAKE_NGINX_TEST_STATUS
  if transactional_switch_slot green; then
    fail "expected nginx test failure"
  fi
  assert_file_contains "${NGINX_ACTIVE_INCLUDE}" 'triangle_cms_slot blue'
  assert_no_switch_temps "${dir}"
}

test_reload_failure_restores_include() {
  local dir reload_count
  make_case reload_fail
  dir="${CASE_DIR}"
  reload_count="${dir}/reload-count"
  write_include_file blue "${NGINX_ACTIVE_INCLUDE}"
  NGINX_RELOAD_CMD='c=$(cat "${FAKE_RELOAD_COUNT_FILE}" 2>/dev/null || echo 0); c=$((c+1)); echo "$c" > "${FAKE_RELOAD_COUNT_FILE}"; if [[ "$c" -eq 1 ]]; then exit 1; fi; exit 0'
  FAKE_RELOAD_COUNT_FILE="${reload_count}"
  export NGINX_RELOAD_CMD FAKE_RELOAD_COUNT_FILE
  if transactional_switch_slot green; then
    fail "expected reload failure"
  fi
  assert_file_contains "${NGINX_ACTIVE_INCLUDE}" 'triangle_cms_slot blue'
  [[ "$(cat "${reload_count}")" == "2" ]] || fail "expected recovery reload"
  assert_no_switch_temps "${dir}"
}

test_missing_include_directory_preflight_fails_before_pull() {
  local dir
  make_case missing_include_dir
  dir="${CASE_DIR}"
  NGINX_ACTIVE_INCLUDE="${dir}/missing/active-upstreams.conf"
  export NGINX_ACTIVE_INCLUDE
  run_deploy_expect_failure_without_docker
}

test_non_writable_include_directory_preflight_fails_before_pull() {
  local dir
  make_case non_writable_include_dir
  dir="${CASE_DIR}"
  chmod 0500 "${dir}/triangle-cms"
  run_deploy_expect_failure_without_docker
  chmod 0750 "${dir}/triangle-cms"
}

test_existing_non_writable_include_preflight_fails_before_pull() {
  local dir
  make_case non_writable_include
  dir="${CASE_DIR}"
  write_include_file blue "${NGINX_ACTIVE_INCLUDE}"
  chmod 0444 "${NGINX_ACTIVE_INCLUDE}"
  run_deploy_expect_failure_without_docker
  chmod 0644 "${NGINX_ACTIVE_INCLUDE}"
}

test_invalid_active_slot_preflight_fails_before_pull() {
  make_case invalid_active_slot
  echo 'set $triangle_cms_slot purple;' > "${NGINX_ACTIVE_INCLUDE}"
  run_deploy_expect_failure_without_docker
}

test_successful_deployment_preflight() {
  make_case successful_preflight
  write_include_file blue "${NGINX_ACTIVE_INCLUDE}"
  deployment_preflight
}

test_nginx_reload_privilege_preflight_fails_before_pull() {
  make_case reload_privilege
  write_include_file blue "${NGINX_ACTIVE_INCLUDE}"
  FAKE_NGINX_RELOAD_CHECK_STATUS=1
  export FAKE_NGINX_RELOAD_CHECK_STATUS
  run_deploy_expect_failure_without_docker
}

test_failed_readiness_leaves_active_slot() {
  local dir sha
  make_case readiness
  dir="${CASE_DIR}"
  write_fake_bin "${dir}"
  write_include_file blue "${NGINX_ACTIVE_INCLUDE}"
  FAIL_READINESS=1
  export FAIL_READINESS
  sha="0123456789abcdef0123456789abcdef01234567"
  if "${SCRIPT_DIR}/deploy.sh" "${sha}"; then
    fail "expected readiness failure"
  fi
  assert_file_contains "${NGINX_ACTIVE_INCLUDE}" 'triangle_cms_slot blue'
}

test_failed_public_smoke_rolls_back() {
  local dir sha
  make_case smoke
  dir="${CASE_DIR}"
  write_fake_bin "${dir}"
  write_include_file blue "${NGINX_ACTIVE_INCLUDE}"
  FAIL_PUBLIC=1
  export FAIL_PUBLIC
  sha="0123456789abcdef0123456789abcdef01234567"
  if "${SCRIPT_DIR}/deploy.sh" "${sha}"; then
    fail "expected public smoke failure"
  fi
  assert_file_contains "${NGINX_ACTIVE_INCLUDE}" 'triangle_cms_slot blue'
}

test_invalid_and_malicious_sha() {
  local dir
  make_case bad_sha
  dir="${CASE_DIR}"
  write_fake_bin "${dir}"
  write_include_file blue "${NGINX_ACTIVE_INCLUDE}"
  if "${SCRIPT_DIR}/deploy.sh" '0123456789abcdef0123456789abcdef0123456;touch-x'; then
    fail "expected malicious sha rejection"
  fi
  if "${SCRIPT_DIR}/deploy.sh" 'not-a-sha'; then
    fail "expected invalid sha rejection"
  fi
}

test_lock_contention() {
  local dir
  make_case lock
  dir="${CASE_DIR}"
  write_fake_bin "${dir}"
  write_include_file blue "${NGINX_ACTIVE_INCLUDE}"
  exec 8>"${DEPLOY_LOCK_FILE}"
  flock -n 8 || fail "failed to acquire test lock"
  if "${SCRIPT_DIR}/rollback.sh" green; then
    fail "expected rollback lock contention failure"
  fi
  exec 8>&-
}

test_host_nginx_health_uses_default_type() {
  local nginx_conf="${SCRIPT_DIR}/../nginx/triangle-cms.conf"
  assert_file_contains "${nginx_conf}" 'default_type text/plain'
  assert_file_not_contains "${nginx_conf}" 'add_header Content-Type text/plain'
}

test_no_old_production_include_path_references() {
  local old_path
  old_path="/etc/nginx/triangle-cms"
  old_path="${old_path}-active-upstreams.conf"
  if git grep -n -- "${old_path}" -- .; then
    fail "found old production include path reference"
  fi
}

test_first_deployment_switch
test_blue_to_green_switch
test_malformed_active_include_fails
test_nginx_test_failure_restores_include
test_reload_failure_restores_include
test_missing_include_directory_preflight_fails_before_pull
test_non_writable_include_directory_preflight_fails_before_pull
test_existing_non_writable_include_preflight_fails_before_pull
test_invalid_active_slot_preflight_fails_before_pull
test_successful_deployment_preflight
test_nginx_reload_privilege_preflight_fails_before_pull
test_failed_readiness_leaves_active_slot
test_failed_public_smoke_rolls_back
test_invalid_and_malicious_sha
test_lock_contention
test_host_nginx_health_uses_default_type
test_no_old_production_include_path_references

echo "deploy script tests passed"
