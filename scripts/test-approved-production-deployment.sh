#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

FAKE_BIN="${TMP_DIR}/bin"
COMMAND_LOG="${TMP_DIR}/commands.log"
COMPOSE_UP_MARKER="${TMP_DIR}/compose-up"
ENV_FILE="${TMP_DIR}/operations.env"
mkdir -p "${FAKE_BIN}"
: > "${COMMAND_LOG}"
: > "${ENV_FILE}"

REVISION="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
WORKFLOW_SHA="dddddddddddddddddddddddddddddddddddddddd"
DIGEST="sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
IMAGE="ghcr.io/mizzz-ivr/vaultsend@${DIGEST}"
REASON_SHA="eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

cat > "${FAKE_BIN}/docker" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'docker %s\n' "$*" >> "${MOCK_COMMAND_LOG}"
case "${1:-}" in
  pull) exit 0 ;;
  image)
    [[ "${2:-}" == "inspect" ]] || exit 2
    if [[ "${3:-}" == "--format" ]]; then
      case "${4:-}" in
        *RepoDigests*) printf '["%s"]\n' "${MOCK_IMAGE_REF}" ;;
        *Labels*) printf '{"org.opencontainers.image.source":"https://github.com/mizzz-ivr/vaultsend","org.opencontainers.image.revision":"%s"}\n' "${MOCK_REVISION}" ;;
        *) exit 2 ;;
      esac
    else
      printf '[{"RepoDigests":["%s"],"Config":{"Labels":{"org.opencontainers.image.source":"https://github.com/mizzz-ivr/vaultsend","org.opencontainers.image.revision":"%s"}}}]\n' "${MOCK_IMAGE_REF}" "${MOCK_REVISION}"
    fi
    ;;
  compose)
    if [[ " $* " == *" up "* ]]; then
      [[ "${MOCK_COMPOSE_UP_FAIL:-false}" != "true" ]] || exit 1
      : > "${MOCK_COMPOSE_UP_MARKER}"
    fi
    ;;
  *) exit 2 ;;
esac
EOF

cat > "${FAKE_BIN}/cosign" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'cosign %s\n' "$*" >> "${MOCK_COMMAND_LOG}"
printf '[{"verified":true}]\n'
EOF

cat > "${FAKE_BIN}/gh" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'gh %s\n' "$*" >> "${MOCK_COMMAND_LOG}"
if [[ "${1:-}" == "auth" && "${2:-}" == "status" ]]; then
  exit 0
fi
if [[ "${1:-}" == "attestation" && "${2:-}" == "verify" ]]; then
  [[ "${MOCK_ATTESTATION_FAIL:-false}" != "true" ]] || exit 1
  printf '[{"verified":true}]\n'
  exit 0
fi
if [[ "${1:-}" == "api" ]]; then
  sha="${MOCK_WORKFLOW_SHA}"
  [[ "${MOCK_WORKFLOW_SHA_MISMATCH:-false}" != "true" ]] || sha="ffffffffffffffffffffffffffffffffffffffff"
  printf '{"event":"workflow_dispatch","head_branch":"main","head_sha":"%s","run_attempt":1,"conclusion":"success","path":".github/workflows/production-deploy.yml"}\n' "${sha}"
  exit 0
fi
exit 2
EOF

chmod +x "${FAKE_BIN}/docker" "${FAKE_BIN}/cosign" "${FAKE_BIN}/gh"

base_env=(
  PATH="${FAKE_BIN}:${PATH}"
  MOCK_COMMAND_LOG="${COMMAND_LOG}"
  MOCK_COMPOSE_UP_MARKER="${COMPOSE_UP_MARKER}"
  MOCK_IMAGE_REF="${IMAGE}"
  MOCK_REVISION="${REVISION}"
  MOCK_WORKFLOW_SHA="${WORKFLOW_SHA}"
  VAULTSEND_COMPOSE_FILE="${ROOT_DIR}/deploy/compose/operations.yml"
  VAULTSEND_COMPOSE_ENV_FILE="${ENV_FILE}"
)

expect_failure() {
  local name="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    echo "FAIL: ${name}" >&2
    exit 1
  fi
  echo "PASS: ${name}"
}

expect_success() {
  local name="$1"
  shift
  if ! "$@" >"${TMP_DIR}/stdout" 2>"${TMP_DIR}/stderr"; then
    echo "FAIL: ${name}" >&2
    cat "${TMP_DIR}/stderr" >&2
    exit 1
  fi
  echo "PASS: ${name}"
}

new_case() {
  rm -f "${COMPOSE_UP_MARKER}"
  : > "${COMMAND_LOG}"
}

create_manifest() {
  local path="$1"
  local authorized_epoch="$2"
  local expires_epoch="$3"
  local workflow_ref="${4:-mizzz-ivr/vaultsend/.github/workflows/production-deploy.yml@refs/heads/main}"
  local change_id="${5:-CHG-2026-0001}"
  jq -n \
    --arg image "${IMAGE}" \
    --arg revision "${REVISION}" \
    --arg change_id "${change_id}" \
    --arg reason_sha "${REASON_SHA}" \
    --arg workflow_ref "${workflow_ref}" \
    --arg workflow_sha "${WORKFLOW_SHA}" \
    --argjson authorized_epoch "${authorized_epoch}" \
    --argjson expires_epoch "${expires_epoch}" \
    '{
      schema_version: "1",
      authorization_status: "approved",
      environment: "production",
      image: $image,
      source_revision: $revision,
      change_request_id: $change_id,
      deployment_reason_sha256: $reason_sha,
      requested_by: "mizzz-ivr",
      workflow: {
        repository: "mizzz-ivr/vaultsend",
        ref: $workflow_ref,
        sha: $workflow_sha,
        run_id: "123456",
        run_attempt: "1"
      },
      authorized_at: "2026-07-30T00:00:00Z",
      authorized_at_epoch: $authorized_epoch,
      expires_at: "2026-07-30T02:00:00Z",
      expires_at_epoch: $expires_epoch
    }' > "${path}"
}

now="$(date -u +%s)"
valid_manifest="${TMP_DIR}/valid.json"
create_manifest "${valid_manifest}" "$((now - 60))" "$((now + 3540))"

new_case
expect_success "許可証のAttestation・Workflow run・イメージを事前確認" \
  env "${base_env[@]}" PRODUCTION_DEPLOYMENT_RESULT_DIR="${TMP_DIR}/check-result" \
    bash "${ROOT_DIR}/scripts/deploy-approved-production.sh" --check "${valid_manifest}"
[[ ! -e "${COMPOSE_UP_MARKER}" ]]
grep -q '^gh attestation verify ' "${COMMAND_LOG}"
grep -q '^gh api repos/mizzz-ivr/vaultsend/actions/runs/123456$' "${COMMAND_LOG}"
grep -q '^docker compose .* config --quiet$' "${COMMAND_LOG}"

expired_manifest="${TMP_DIR}/expired.json"
create_manifest "${expired_manifest}" "$((now - 7200))" "$((now - 3600))"
new_case
expect_failure "期限切れ許可証を拒否" \
  env "${base_env[@]}" PRODUCTION_DEPLOYMENT_RESULT_DIR="${TMP_DIR}/expired-result" \
    bash "${ROOT_DIR}/scripts/deploy-approved-production.sh" --check "${expired_manifest}"

wrong_ref_manifest="${TMP_DIR}/wrong-ref.json"
create_manifest "${wrong_ref_manifest}" "$((now - 60))" "$((now + 3540))" \
  'mizzz-ivr/vaultsend/.github/workflows/other.yml@refs/heads/main'
new_case
expect_failure "想定外Workflow refを拒否" \
  env "${base_env[@]}" PRODUCTION_DEPLOYMENT_RESULT_DIR="${TMP_DIR}/wrong-ref-result" \
    bash "${ROOT_DIR}/scripts/deploy-approved-production.sh" --check "${wrong_ref_manifest}"

new_case
expect_failure "許可manifestのAttestation失敗を拒否" \
  env "${base_env[@]}" MOCK_ATTESTATION_FAIL=true \
    PRODUCTION_DEPLOYMENT_RESULT_DIR="${TMP_DIR}/attestation-result" \
    bash "${ROOT_DIR}/scripts/deploy-approved-production.sh" --check "${valid_manifest}"

new_case
expect_failure "Workflow runのsource SHA不一致を拒否" \
  env "${base_env[@]}" MOCK_WORKFLOW_SHA_MISMATCH=true \
    PRODUCTION_DEPLOYMENT_RESULT_DIR="${TMP_DIR}/run-result" \
    bash "${ROOT_DIR}/scripts/deploy-approved-production.sh" --check "${valid_manifest}"

ledger="${TMP_DIR}/ledger"
new_case
expect_success "未使用の許可証で一度だけデプロイ" \
  env "${base_env[@]}" \
    PRODUCTION_DEPLOYMENT_RESULT_DIR="${TMP_DIR}/deploy-result" \
    PRODUCTION_AUTHORIZATION_LEDGER_DIR="${ledger}" \
    bash "${ROOT_DIR}/scripts/deploy-approved-production.sh" --deploy "${valid_manifest}"
[[ -e "${COMPOSE_UP_MARKER}" ]]
[[ "$(find "${ledger}" -maxdepth 1 -name '*.used.json' | wc -l)" -eq 1 ]]

new_case
expect_failure "使用済み許可証の再利用を拒否" \
  env "${base_env[@]}" \
    PRODUCTION_DEPLOYMENT_RESULT_DIR="${TMP_DIR}/replay-result" \
    PRODUCTION_AUTHORIZATION_LEDGER_DIR="${ledger}" \
    bash "${ROOT_DIR}/scripts/deploy-approved-production.sh" --deploy "${valid_manifest}"
[[ ! -e "${COMPOSE_UP_MARKER}" ]]

failure_manifest="${TMP_DIR}/failure.json"
create_manifest "${failure_manifest}" "$((now - 60))" "$((now + 3540))" \
  'mizzz-ivr/vaultsend/.github/workflows/production-deploy.yml@refs/heads/main' 'CHG-2026-FAIL'
failure_ledger="${TMP_DIR}/failure-ledger"
new_case
expect_failure "Compose失敗後も使用開始記録を保持" \
  env "${base_env[@]}" MOCK_COMPOSE_UP_FAIL=true \
    PRODUCTION_DEPLOYMENT_RESULT_DIR="${TMP_DIR}/failure-result" \
    PRODUCTION_AUTHORIZATION_LEDGER_DIR="${failure_ledger}" \
    bash "${ROOT_DIR}/scripts/deploy-approved-production.sh" --deploy "${failure_manifest}"
[[ "$(find "${failure_ledger}" -maxdepth 1 -name '*.started.json' | wc -l)" -eq 1 ]]

new_case
expect_failure "途中失敗した許可証の再利用を拒否" \
  env "${base_env[@]}" \
    PRODUCTION_DEPLOYMENT_RESULT_DIR="${TMP_DIR}/retry-result" \
    PRODUCTION_AUTHORIZATION_LEDGER_DIR="${failure_ledger}" \
    bash "${ROOT_DIR}/scripts/deploy-approved-production.sh" --deploy "${failure_manifest}"
[[ ! -e "${COMPOSE_UP_MARKER}" ]]

echo 'Attestation付き本番デプロイ許可証のmockテストに成功しました。'
