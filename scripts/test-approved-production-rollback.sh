#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

FAKE_BIN="${TMP_DIR}/bin"
COMMAND_LOG="${TMP_DIR}/commands.log"
COMPOSE_UP_MARKER="${TMP_DIR}/compose-up"
RUNNING_IMAGE_FILE="${TMP_DIR}/running-image"
ENV_FILE="${TMP_DIR}/operations.env"
mkdir -p "${FAKE_BIN}"
: > "${COMMAND_LOG}"
: > "${ENV_FILE}"

OLD_REVISION="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
CURRENT_REVISION="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
WORKFLOW_SHA="eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
OLD_DIGEST="sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
CURRENT_DIGEST="sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
UNKNOWN_DIGEST="sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
OLD_IMAGE="ghcr.io/mizzz-ivr/vaultsend@${OLD_DIGEST}"
CURRENT_IMAGE="ghcr.io/mizzz-ivr/vaultsend@${CURRENT_DIGEST}"
UNKNOWN_IMAGE="ghcr.io/mizzz-ivr/vaultsend@${UNKNOWN_DIGEST}"
REASON_SHA="9999999999999999999999999999999999999999999999999999999999999999"
printf '%s\n' "${CURRENT_IMAGE}" > "${RUNNING_IMAGE_FILE}"

cat > "${FAKE_BIN}/docker" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'docker %s\n' "$*" >> "${MOCK_COMMAND_LOG}"

revision_for_image() {
  case "$1" in
    "${MOCK_OLD_IMAGE}") printf '%s\n' "${MOCK_OLD_REVISION}" ;;
    "${MOCK_CURRENT_IMAGE}") printf '%s\n' "${MOCK_CURRENT_REVISION}" ;;
    "${MOCK_UNKNOWN_IMAGE}") printf '%s\n' "${MOCK_UNKNOWN_REVISION:-ffffffffffffffffffffffffffffffffffffffff}" ;;
    *) exit 2 ;;
  esac
}

case "${1:-}" in
  pull)
    exit 0
    ;;
  image)
    [[ "${2:-}" == "inspect" ]] || exit 2
    image_arg="${5:-${3:-}}"
    revision="$(revision_for_image "${image_arg}")"
    if [[ "${3:-}" == "--format" ]]; then
      case "${4:-}" in
        *RepoDigests*) printf '["%s"]\n' "${image_arg}" ;;
        *Labels*) printf '{"org.opencontainers.image.source":"https://github.com/mizzz-ivr/vaultsend","org.opencontainers.image.revision":"%s"}\n' "${revision}" ;;
        *) exit 2 ;;
      esac
    else
      printf '[{"RepoDigests":["%s"],"Config":{"Labels":{"org.opencontainers.image.source":"https://github.com/mizzz-ivr/vaultsend","org.opencontainers.image.revision":"%s"}}}]\n' "${image_arg}" "${revision}"
    fi
    ;;
  inspect)
    [[ "${2:-}" == "--format" ]] || exit 2
    cat "${MOCK_RUNNING_IMAGE_FILE}"
    ;;
  compose)
    if [[ " $* " == *" ps -q audit-worker "* ]]; then
      printf 'audit-worker-container\n'
      exit 0
    fi
    if [[ " $* " == *" up "* ]]; then
      [[ "${MOCK_COMPOSE_UP_FAIL:-false}" != "true" ]] || exit 1
      printf '%s\n' "${VAULTSEND_IMAGE}" > "${MOCK_RUNNING_IMAGE_FILE}"
      : > "${MOCK_COMPOSE_UP_MARKER}"
    fi
    ;;
  *)
    exit 2
    ;;
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
  path='.github/workflows/production-rollback.yml'
  [[ "${MOCK_WORKFLOW_RUN_MISMATCH:-false}" != "true" ]] || path='.github/workflows/other.yml'
  printf '{"event":"workflow_dispatch","head_branch":"main","head_sha":"%s","run_attempt":1,"conclusion":"success","path":"%s"}\n' "${sha}" "${path}"
  exit 0
fi
exit 2
EOF

cat > "${FAKE_BIN}/git" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'git %s\n' "$*" >> "${MOCK_COMMAND_LOG}"
case "${1:-}" in
  fetch)
    exit 0
    ;;
  rev-list)
    printf '%s\n%s\n' "${MOCK_CURRENT_REVISION}" "${MOCK_OLD_REVISION}"
    ;;
  merge-base)
    [[ "${MOCK_ANCESTRY_FAIL:-false}" != "true" ]]
    ;;
  *)
    exit 2
    ;;
esac
EOF

chmod +x "${FAKE_BIN}/docker" "${FAKE_BIN}/cosign" "${FAKE_BIN}/gh" "${FAKE_BIN}/git"

base_env=(
  PATH="${FAKE_BIN}:${PATH}"
  MOCK_COMMAND_LOG="${COMMAND_LOG}"
  MOCK_COMPOSE_UP_MARKER="${COMPOSE_UP_MARKER}"
  MOCK_RUNNING_IMAGE_FILE="${RUNNING_IMAGE_FILE}"
  MOCK_OLD_IMAGE="${OLD_IMAGE}"
  MOCK_CURRENT_IMAGE="${CURRENT_IMAGE}"
  MOCK_UNKNOWN_IMAGE="${UNKNOWN_IMAGE}"
  MOCK_OLD_REVISION="${OLD_REVISION}"
  MOCK_CURRENT_REVISION="${CURRENT_REVISION}"
  MOCK_WORKFLOW_SHA="${WORKFLOW_SHA}"
  VAULTSEND_COMPOSE_FILE="${ROOT_DIR}/deploy/compose/operations.yml"
  VAULTSEND_COMPOSE_ENV_FILE="${ENV_FILE}"
)

expect_failure() {
  local name="$1"
  shift
  if "$@" >"${TMP_DIR}/stdout" 2>"${TMP_DIR}/stderr"; then
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

create_release_event() {
  local path="$1"
  local event_id="$2"
  local operation="$3"
  local image="$4"
  local revision="$5"
  local previous_json="$6"
  local completed_epoch_ms="$7"
  jq -n \
    --arg event_id "${event_id}" \
    --arg operation "${operation}" \
    --arg image "${image}" \
    --arg revision "${revision}" \
    --argjson previous "${previous_json}" \
    --arg completed_at "2026-07-31T00:00:00Z" \
    --argjson completed_epoch_ms "${completed_epoch_ms}" \
    '{
      schema_version: "1",
      event_id: $event_id,
      status: "success",
      operation: $operation,
      target: {image: $image, source_revision: $revision},
      previous: $previous,
      authorization: {
        manifest_sha256: $event_id,
        change_request_id: "CHG-SEED",
        workflow_run_id: "1",
        workflow_run_attempt: "1"
      },
      completed_at: $completed_at,
      completed_at_epoch_ms: $completed_epoch_ms,
      completed_by: "deployer"
    }' > "${path}"
}

seed_release_ledger() {
  local ledger="$1"
  local old_event="${TMP_DIR}/old-event-$(basename "${ledger}").json"
  local current_event="${TMP_DIR}/current-event-$(basename "${ledger}").json"
  local old_id="1111111111111111111111111111111111111111111111111111111111111111"
  local current_id="2222222222222222222222222222222222222222222222222222222222222222"
  create_release_event "${old_event}" "${old_id}" deployment "${OLD_IMAGE}" "${OLD_REVISION}" null 1000
  bash "${ROOT_DIR}/scripts/manage-production-release-ledger.sh" record-success "${ledger}" "${old_event}" >/dev/null
  previous="$(jq -c '{event_id, image: .target.image, source_revision: .target.source_revision}' "${ledger}/current-release.json")"
  create_release_event "${current_event}" "${current_id}" deployment "${CURRENT_IMAGE}" "${CURRENT_REVISION}" "${previous}" 2000
  bash "${ROOT_DIR}/scripts/manage-production-release-ledger.sh" record-success "${ledger}" "${current_event}" >/dev/null
}

create_manifest() {
  local path="$1"
  local current_image="$2"
  local current_revision="$3"
  local target_image="$4"
  local target_revision="$5"
  local authorized_epoch="$6"
  local expires_epoch="$7"
  local change_id="${8:-CHG-ROLLBACK-0001}"
  jq -n \
    --arg current_image "${current_image}" \
    --arg current_revision "${current_revision}" \
    --arg target_image "${target_image}" \
    --arg target_revision "${target_revision}" \
    --arg reason_sha "${REASON_SHA}" \
    --arg change_id "${change_id}" \
    --arg workflow_sha "${WORKFLOW_SHA}" \
    --argjson authorized_epoch "${authorized_epoch}" \
    --argjson expires_epoch "${expires_epoch}" \
    '{
      schema_version: "1",
      authorization_type: "rollback",
      authorization_status: "approved",
      environment: "production-rollback",
      expected_current: {image: $current_image, source_revision: $current_revision},
      target: {image: $target_image, source_revision: $target_revision},
      change_request_id: $change_id,
      rollback_reason_sha256: $reason_sha,
      requested_by: "mizzz-ivr",
      workflow: {
        repository: "mizzz-ivr/vaultsend",
        ref: "mizzz-ivr/vaultsend/.github/workflows/production-rollback.yml@refs/heads/main",
        sha: $workflow_sha,
        run_id: "654321",
        run_attempt: "1"
      },
      authorized_at: "2026-07-31T00:00:00Z",
      authorized_at_epoch: $authorized_epoch,
      expires_at: "2026-07-31T00:30:00Z",
      expires_at_epoch: $expires_epoch
    }' > "${path}"
}

now="$(date -u +%s)"
release_ledger="${TMP_DIR}/release-ledger"
seed_release_ledger "${release_ledger}"
valid_manifest="${TMP_DIR}/valid-rollback.json"
create_manifest "${valid_manifest}" "${CURRENT_IMAGE}" "${CURRENT_REVISION}" "${OLD_IMAGE}" "${OLD_REVISION}" "$((now - 30))" "$((now + 1770))"

new_case
expect_success "現在release・過去履歴・許可証・イメージを事前確認" \
  env "${base_env[@]}" \
    PRODUCTION_RELEASE_LEDGER_DIR="${release_ledger}" \
    PRODUCTION_ROLLBACK_RESULT_DIR="${TMP_DIR}/check-result" \
    bash "${ROOT_DIR}/scripts/rollback-approved-production.sh" --check "${valid_manifest}"
[[ ! -e "${COMPOSE_UP_MARKER}" ]]
grep -q '^gh attestation verify ' "${COMMAND_LOG}"
grep -q '^git merge-base --is-ancestor ' "${COMMAND_LOG}"

unknown_manifest="${TMP_DIR}/unknown.json"
create_manifest "${unknown_manifest}" "${CURRENT_IMAGE}" "${CURRENT_REVISION}" "${UNKNOWN_IMAGE}" "ffffffffffffffffffffffffffffffffffffffff" "$((now - 30))" "$((now + 1770))" 'CHG-UNKNOWN'
new_case
expect_failure "正常デプロイ履歴にないdigestを拒否" \
  env "${base_env[@]}" PRODUCTION_RELEASE_LEDGER_DIR="${release_ledger}" \
    PRODUCTION_ROLLBACK_RESULT_DIR="${TMP_DIR}/unknown-result" \
    bash "${ROOT_DIR}/scripts/rollback-approved-production.sh" --check "${unknown_manifest}"

mismatch_manifest="${TMP_DIR}/current-mismatch.json"
create_manifest "${mismatch_manifest}" "${OLD_IMAGE}" "${OLD_REVISION}" "${CURRENT_IMAGE}" "${CURRENT_REVISION}" "$((now - 30))" "$((now + 1770))" 'CHG-MISMATCH'
new_case
expect_failure "台帳currentと許可証の戻し元不一致を拒否" \
  env "${base_env[@]}" PRODUCTION_RELEASE_LEDGER_DIR="${release_ledger}" \
    PRODUCTION_ROLLBACK_RESULT_DIR="${TMP_DIR}/mismatch-result" \
    bash "${ROOT_DIR}/scripts/rollback-approved-production.sh" --check "${mismatch_manifest}"

printf '%s\n' "${OLD_IMAGE}" > "${RUNNING_IMAGE_FILE}"
new_case
expect_failure "実稼働imageとcurrent台帳の不一致を拒否" \
  env "${base_env[@]}" PRODUCTION_RELEASE_LEDGER_DIR="${release_ledger}" \
    PRODUCTION_ROLLBACK_RESULT_DIR="${TMP_DIR}/running-mismatch-result" \
    bash "${ROOT_DIR}/scripts/rollback-approved-production.sh" --check "${valid_manifest}"
printf '%s\n' "${CURRENT_IMAGE}" > "${RUNNING_IMAGE_FILE}"

expired_manifest="${TMP_DIR}/expired.json"
create_manifest "${expired_manifest}" "${CURRENT_IMAGE}" "${CURRENT_REVISION}" "${OLD_IMAGE}" "${OLD_REVISION}" "$((now - 3600))" "$((now - 1800))" 'CHG-EXPIRED'
new_case
expect_failure "期限切れロールバック許可証を拒否" \
  env "${base_env[@]}" PRODUCTION_RELEASE_LEDGER_DIR="${release_ledger}" \
    PRODUCTION_ROLLBACK_RESULT_DIR="${TMP_DIR}/expired-result" \
    bash "${ROOT_DIR}/scripts/rollback-approved-production.sh" --check "${expired_manifest}"

new_case
expect_failure "許可manifestのAttestation失敗を拒否" \
  env "${base_env[@]}" MOCK_ATTESTATION_FAIL=true PRODUCTION_RELEASE_LEDGER_DIR="${release_ledger}" \
    PRODUCTION_ROLLBACK_RESULT_DIR="${TMP_DIR}/attestation-result" \
    bash "${ROOT_DIR}/scripts/rollback-approved-production.sh" --check "${valid_manifest}"

new_case
expect_failure "許可証Workflow run不一致を拒否" \
  env "${base_env[@]}" MOCK_WORKFLOW_RUN_MISMATCH=true PRODUCTION_RELEASE_LEDGER_DIR="${release_ledger}" \
    PRODUCTION_ROLLBACK_RESULT_DIR="${TMP_DIR}/run-result" \
    bash "${ROOT_DIR}/scripts/rollback-approved-production.sh" --check "${valid_manifest}"

new_case
expect_failure "戻し先が戻し元の祖先でない場合を拒否" \
  env "${base_env[@]}" MOCK_ANCESTRY_FAIL=true PRODUCTION_RELEASE_LEDGER_DIR="${release_ledger}" \
    PRODUCTION_ROLLBACK_RESULT_DIR="${TMP_DIR}/ancestry-result" \
    bash "${ROOT_DIR}/scripts/rollback-approved-production.sh" --check "${valid_manifest}"

rollback_ledger="${TMP_DIR}/rollback-authorizations"
new_case
expect_success "正常デプロイ済みの過去releaseへ一度だけロールバック" \
  env "${base_env[@]}" PRODUCTION_RELEASE_LEDGER_DIR="${release_ledger}" \
    PRODUCTION_ROLLBACK_AUTHORIZATION_LEDGER_DIR="${rollback_ledger}" \
    PRODUCTION_ROLLBACK_RESULT_DIR="${TMP_DIR}/deploy-result" \
    bash "${ROOT_DIR}/scripts/rollback-approved-production.sh" --deploy "${valid_manifest}"
[[ -e "${COMPOSE_UP_MARKER}" ]]
[[ "$(cat "${RUNNING_IMAGE_FILE}")" == "${OLD_IMAGE}" ]]
jq -e \
  --arg image "${OLD_IMAGE}" \
  --arg revision "${OLD_REVISION}" \
  '.operation == "rollback" and .target.image == $image and .target.source_revision == $revision and .previous.image != $image' \
  "${release_ledger}/current-release.json" >/dev/null
[[ "$(find "${release_ledger}/events" -maxdepth 1 -name '*.json' | wc -l)" -eq 3 ]]
[[ "$(find "${rollback_ledger}" -maxdepth 1 -name '*.used.json' | wc -l)" -eq 1 ]]

new_case
expect_failure "使用済みロールバック許可証の再利用を拒否" \
  env "${base_env[@]}" PRODUCTION_RELEASE_LEDGER_DIR="${release_ledger}" \
    PRODUCTION_ROLLBACK_AUTHORIZATION_LEDGER_DIR="${rollback_ledger}" \
    PRODUCTION_ROLLBACK_RESULT_DIR="${TMP_DIR}/replay-result" \
    bash "${ROOT_DIR}/scripts/rollback-approved-production.sh" --deploy "${valid_manifest}"

failure_release_ledger="${TMP_DIR}/failure-release-ledger"
seed_release_ledger "${failure_release_ledger}"
printf '%s\n' "${CURRENT_IMAGE}" > "${RUNNING_IMAGE_FILE}"
failure_manifest="${TMP_DIR}/failure-manifest.json"
create_manifest "${failure_manifest}" "${CURRENT_IMAGE}" "${CURRENT_REVISION}" "${OLD_IMAGE}" "${OLD_REVISION}" "$((now - 30))" "$((now + 1770))" 'CHG-ROLLBACK-FAIL'
failure_auth_ledger="${TMP_DIR}/failure-rollback-authorizations"
new_case
expect_failure "Compose失敗後は開始記録を保持しrelease台帳を更新しない" \
  env "${base_env[@]}" MOCK_COMPOSE_UP_FAIL=true \
    PRODUCTION_RELEASE_LEDGER_DIR="${failure_release_ledger}" \
    PRODUCTION_ROLLBACK_AUTHORIZATION_LEDGER_DIR="${failure_auth_ledger}" \
    PRODUCTION_ROLLBACK_RESULT_DIR="${TMP_DIR}/failure-result" \
    bash "${ROOT_DIR}/scripts/rollback-approved-production.sh" --deploy "${failure_manifest}"
[[ "$(find "${failure_auth_ledger}" -maxdepth 1 -name '*.started.json' | wc -l)" -eq 1 ]]
jq -e --arg image "${CURRENT_IMAGE}" '.target.image == $image' "${failure_release_ledger}/current-release.json" >/dev/null

new_case
expect_failure "途中失敗したロールバック許可証の再利用を拒否" \
  env "${base_env[@]}" \
    PRODUCTION_RELEASE_LEDGER_DIR="${failure_release_ledger}" \
    PRODUCTION_ROLLBACK_AUTHORIZATION_LEDGER_DIR="${failure_auth_ledger}" \
    PRODUCTION_ROLLBACK_RESULT_DIR="${TMP_DIR}/failure-retry-result" \
    bash "${ROOT_DIR}/scripts/rollback-approved-production.sh" --deploy "${failure_manifest}"

echo '承認済み本番ロールバックとrelease台帳のmockテストに成功しました。'
