#!/usr/bin/env bash
set -Eeuo pipefail

umask 077

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="${1:---check}"
MANIFEST_PATH="${2:-${PRODUCTION_AUTHORIZATION_MANIFEST:-}}"
EXPECTED_GITHUB_REPOSITORY="${EXPECTED_GITHUB_REPOSITORY:-mizzz-ivr/vaultsend}"
EXPECTED_IMAGE_REPOSITORY="${EXPECTED_IMAGE_REPOSITORY:-ghcr.io/mizzz-ivr/vaultsend}"
EXPECTED_SIGNER_WORKFLOW="${EXPECTED_PRODUCTION_SIGNER_WORKFLOW:-mizzz-ivr/vaultsend/.github/workflows/production-deploy.yml}"
EXPECTED_WORKFLOW_REF="${EXPECTED_PRODUCTION_WORKFLOW_REF:-mizzz-ivr/vaultsend/.github/workflows/production-deploy.yml@refs/heads/main}"
EXPECTED_SOURCE_REF="${EXPECTED_PRODUCTION_SOURCE_REF:-refs/heads/main}"
EXPECTED_OIDC_ISSUER="${EXPECTED_OIDC_ISSUER:-https://token.actions.githubusercontent.com}"
RESULT_DIR="${PRODUCTION_DEPLOYMENT_RESULT_DIR:-artifacts/production-deployment/execution}"
LEDGER_DIR="${PRODUCTION_AUTHORIZATION_LEDGER_DIR:-}"
RELEASE_LEDGER_DIR="${PRODUCTION_RELEASE_LEDGER_DIR:-${LEDGER_DIR:+${LEDGER_DIR}/releases}}"
MAX_AUTHORIZATION_TTL_SEC="${PRODUCTION_AUTHORIZATION_MAX_TTL_SEC:-7200}"

usage() {
  cat <<'EOF'
Usage:
  bash scripts/deploy-approved-production.sh --check /secure/path/authorization-manifest.json

  PRODUCTION_AUTHORIZATION_LEDGER_DIR=/var/lib/vaultsend/deployment-authorizations \
  PRODUCTION_RELEASE_LEDGER_DIR=/var/lib/vaultsend/releases \
    bash scripts/deploy-approved-production.sh --deploy \
      /secure/path/authorization-manifest.json

Modes:
  --check   許可証・Workflow run・イメージ・Compose設定を検証する。起動しない。
  --deploy  上記を再検証し、未使用の許可証でのみComposeを起動してrelease台帳を更新する。
EOF
}

fail() {
  echo "承認済み本番デプロイエラー: $*" >&2
  exit 1
}

require_command() {
  local command_name="$1"
  command -v "${command_name}" >/dev/null 2>&1 \
    || fail "必要なコマンドが見つかりません: ${command_name}"
}

case "${MODE}" in
  --check|--deploy) ;;
  -h|--help)
    usage
    exit 0
    ;;
  *)
    usage >&2
    fail "不明なmodeです: ${MODE}"
    ;;
esac

if [[ -z "${MANIFEST_PATH}" ]]; then
  usage >&2
  fail "デプロイ許可manifestを指定してください"
fi
if [[ ! -f "${MANIFEST_PATH}" || -L "${MANIFEST_PATH}" ]]; then
  fail "許可manifestが通常ファイルではありません: ${MANIFEST_PATH}"
fi
if [[ ! "${MAX_AUTHORIZATION_TTL_SEC}" =~ ^[0-9]+$ || "${MAX_AUTHORIZATION_TTL_SEC}" -lt 300 || "${MAX_AUTHORIZATION_TTL_SEC}" -gt 86400 ]]; then
  fail "許可証の最大TTL設定が不正です: ${MAX_AUTHORIZATION_TTL_SEC}"
fi

for command_name in awk cosign cp date docker flock gh id jq mkdir mv rm rmdir sha256sum; do
  require_command "${command_name}"
done

if [[ -L "${RESULT_DIR}" ]]; then
  fail "デプロイ結果ディレクトリにsymbolic linkは使用できません: ${RESULT_DIR}"
fi
mkdir -p "${RESULT_DIR}"
chmod 700 "${RESULT_DIR}"

manifest_abs="$(cd "$(dirname "${MANIFEST_PATH}")" && pwd)/$(basename "${MANIFEST_PATH}")"
manifest_sha256="$(sha256sum "${manifest_abs}" | awk '{print $1}')"

jq -e . "${manifest_abs}" >/dev/null || fail "許可manifestがJSONではありません"

schema_version="$(jq -er '.schema_version // empty' "${manifest_abs}")" || fail "schema_versionを取得できません"
authorization_status="$(jq -er '.authorization_status // empty' "${manifest_abs}")" || fail "authorization_statusを取得できません"
environment_name="$(jq -er '.environment // empty' "${manifest_abs}")" || fail "environmentを取得できません"
image_ref="$(jq -er '.image // empty' "${manifest_abs}")" || fail "imageを取得できません"
source_revision="$(jq -er '.source_revision // empty' "${manifest_abs}")" || fail "source_revisionを取得できません"
change_request_id="$(jq -er '.change_request_id // empty' "${manifest_abs}")" || fail "change_request_idを取得できません"
reason_sha256="$(jq -er '.deployment_reason_sha256 // empty' "${manifest_abs}")" || fail "deployment_reason_sha256を取得できません"
workflow_repository="$(jq -er '.workflow.repository // empty' "${manifest_abs}")" || fail "workflow.repositoryを取得できません"
workflow_ref="$(jq -er '.workflow.ref // empty' "${manifest_abs}")" || fail "workflow.refを取得できません"
workflow_sha="$(jq -er '.workflow.sha // empty' "${manifest_abs}")" || fail "workflow SHAを取得できません"
workflow_run_id="$(jq -er '.workflow.run_id // empty' "${manifest_abs}")" || fail "workflow.run_idを取得できません"
workflow_run_attempt="$(jq -er '.workflow.run_attempt // empty' "${manifest_abs}")" || fail "workflow.run_attemptを取得できません"
authorized_at_epoch="$(jq -er '.authorized_at_epoch // empty' "${manifest_abs}")" || fail "authorized_at_epochを取得できません"
expires_at_epoch="$(jq -er '.expires_at_epoch // empty' "${manifest_abs}")" || fail "expires_at_epochを取得できません"

[[ "${schema_version}" == "1" ]] || fail "未対応の許可manifest schemaです: ${schema_version}"
[[ "${authorization_status}" == "approved" ]] || fail "許可状態がapprovedではありません: ${authorization_status}"
[[ "${environment_name}" == "production" ]] || fail "許可環境がproductionではありません: ${environment_name}"
[[ "${workflow_repository}" == "${EXPECTED_GITHUB_REPOSITORY}" ]] || fail "workflow repositoryが期待値と一致しません: ${workflow_repository}"
[[ "${workflow_ref}" == "${EXPECTED_WORKFLOW_REF}" ]] || fail "workflow refが期待値と一致しません: ${workflow_ref}"
[[ "${image_ref}" =~ ^ghcr\.io/[a-z0-9._-]+/[a-z0-9._-]+@sha256:[0-9a-f]{64}$ ]] || fail "許可イメージ形式が不正です: ${image_ref}"
[[ "${image_ref%@*}" == "${EXPECTED_IMAGE_REPOSITORY}" ]] || fail "許可イメージrepositoryが期待値と一致しません: ${image_ref%@*}"
[[ "${source_revision}" =~ ^[0-9a-f]{40}$ ]] || fail "source revision形式が不正です: ${source_revision}"
[[ "${workflow_sha}" =~ ^[0-9a-f]{40}$ ]] || fail "workflow SHA形式が不正です: ${workflow_sha}"
[[ "${workflow_run_id}" =~ ^[0-9]+$ && "${workflow_run_attempt}" =~ ^[0-9]+$ ]] || fail "Workflow run情報が不正です"
[[ "${change_request_id}" =~ ^[A-Za-z0-9][A-Za-z0-9._:/-]{2,79}$ ]] || fail "変更管理番号形式が不正です"
[[ "${reason_sha256}" =~ ^[0-9a-f]{64}$ ]] || fail "デプロイ理由digest形式が不正です"
[[ "${authorized_at_epoch}" =~ ^[0-9]+$ && "${expires_at_epoch}" =~ ^[0-9]+$ ]] || fail "許可時刻形式が不正です"

now_epoch="$(date -u +%s)"
authorization_ttl=$((expires_at_epoch - authorized_at_epoch))
(( authorization_ttl > 0 && authorization_ttl <= MAX_AUTHORIZATION_TTL_SEC )) \
  || fail "許可証TTLが許容範囲外です: ${authorization_ttl}秒"
(( now_epoch >= authorized_at_epoch - 300 )) || fail "許可開始時刻が未来です"
(( now_epoch <= expires_at_epoch )) || fail "デプロイ許可証が失効しています"

if ! gh auth status >/dev/null 2>&1; then
  fail "GitHub CLIが認証されていません。gh auth loginまたはGH_TOKENを設定してください"
fi

echo "デプロイ許可manifestのArtifact Attestationを検証します"
gh attestation verify "${manifest_abs}" \
  --repo "${EXPECTED_GITHUB_REPOSITORY}" \
  --signer-workflow "${EXPECTED_SIGNER_WORKFLOW}" \
  --source-ref "${EXPECTED_SOURCE_REF}" \
  --source-digest "${workflow_sha}" \
  --cert-oidc-issuer "${EXPECTED_OIDC_ISSUER}" \
  --deny-self-hosted-runners \
  --format json \
  > "${RESULT_DIR}/authorization-attestation-verification.json"

jq -e 'type == "array" and length > 0' \
  "${RESULT_DIR}/authorization-attestation-verification.json" >/dev/null \
  || fail "許可manifestのAttestation検証結果が空です"

echo "許可証を発行したWorkflow runを検証します"
gh api "repos/${EXPECTED_GITHUB_REPOSITORY}/actions/runs/${workflow_run_id}" \
  > "${RESULT_DIR}/authorization-workflow-run.json"

jq -e \
  --arg sha "${workflow_sha}" \
  --argjson attempt "${workflow_run_attempt}" \
  '.event == "workflow_dispatch" and
   .head_branch == "main" and
   .head_sha == $sha and
   .run_attempt == $attempt and
   .conclusion == "success" and
   (.path == ".github/workflows/production-deploy.yml" or
    .path == "mizzz-ivr/vaultsend/.github/workflows/production-deploy.yml@refs/heads/main")' \
  "${RESULT_DIR}/authorization-workflow-run.json" >/dev/null \
  || fail "許可証のWorkflow runが期待条件を満たしません"

cp "${manifest_abs}" "${RESULT_DIR}/authorization-manifest.json"
printf '%s\n' "${manifest_sha256}" > "${RESULT_DIR}/authorization-manifest.sha256"

export EXPECTED_SOURCE_REVISION="${source_revision}"
export VAULTSEND_IMAGE="${image_ref}"
export DEPLOY_VERIFICATION_DIR="${RESULT_DIR}/release-verification"

if [[ "${MODE}" == "--check" ]]; then
  bash "${ROOT_DIR}/scripts/deploy-verified-compose.sh" --check "${image_ref}"
  echo "承認済み本番デプロイの事前確認に成功しました。--checkのため起動していません。"
  exit 0
fi

if [[ -z "${LEDGER_DIR}" ]]; then
  fail "--deployではPRODUCTION_AUTHORIZATION_LEDGER_DIRを指定してください"
fi
if [[ -z "${RELEASE_LEDGER_DIR}" ]]; then
  fail "--deployではPRODUCTION_RELEASE_LEDGER_DIRを指定してください"
fi
if [[ -L "${LEDGER_DIR}" || -L "${RELEASE_LEDGER_DIR}" ]]; then
  fail "許可証台帳・release台帳にsymbolic linkは使用できません"
fi
mkdir -p "${LEDGER_DIR}" "${RELEASE_LEDGER_DIR}"
chmod 700 "${LEDGER_DIR}" "${RELEASE_LEDGER_DIR}"

bash "${ROOT_DIR}/scripts/manage-production-release-ledger.sh" validate "${RELEASE_LEDGER_DIR}" >/dev/null
release_lock_file="${RELEASE_LEDGER_DIR}/deployment.lock"
[[ ! -L "${release_lock_file}" ]] || fail "release台帳lockにsymbolic linkは使用できません"
exec 9>"${release_lock_file}"
chmod 600 "${release_lock_file}"
flock -n 9 || fail "別の本番デプロイまたはrollbackが実行中です"
bash "${ROOT_DIR}/scripts/manage-production-release-ledger.sh" validate "${RELEASE_LEDGER_DIR}" >/dev/null

current_file="${RELEASE_LEDGER_DIR}/current-release.json"
previous_release='null'
if [[ -f "${current_file}" ]]; then
  previous_release="$(jq -c '{event_id, image: .target.image, source_revision: .target.source_revision}' "${current_file}")"
fi

started_record="${LEDGER_DIR}/${manifest_sha256}.started.json"
used_record="${LEDGER_DIR}/${manifest_sha256}.used.json"
lock_dir="${LEDGER_DIR}/${manifest_sha256}.lock"
[[ ! -e "${started_record}" && ! -e "${used_record}" ]] \
  || fail "このデプロイ許可証は使用済み、または過去に使用開始されています"
mkdir "${lock_dir}" 2>/dev/null || fail "このデプロイ許可証は別処理で使用中です"
cleanup_lock() {
  rmdir "${lock_dir}" 2>/dev/null || true
}
trap cleanup_lock EXIT

started_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
started_by="$(id -un)"
jq -n \
  --arg status "started" \
  --arg manifest_sha256 "${manifest_sha256}" \
  --arg image "${image_ref}" \
  --arg source_revision "${source_revision}" \
  --arg change_request_id "${change_request_id}" \
  --arg workflow_run_id "${workflow_run_id}" \
  --arg workflow_run_attempt "${workflow_run_attempt}" \
  --arg started_by "${started_by}" \
  --arg started_at "${started_at}" \
  '{
    status: $status,
    authorization_manifest_sha256: $manifest_sha256,
    image: $image,
    source_revision: $source_revision,
    change_request_id: $change_request_id,
    workflow_run_id: $workflow_run_id,
    workflow_run_attempt: $workflow_run_attempt,
    started_by: $started_by,
    started_at: $started_at
  }' > "${started_record}"
chmod 600 "${started_record}"

bash "${ROOT_DIR}/scripts/deploy-verified-compose.sh" --deploy "${image_ref}"

deployed_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
deployed_at_epoch_ms="$(date -u +%s%3N)"
release_event="${RESULT_DIR}/release-event.json"
jq -n \
  --arg event_id "${manifest_sha256}" \
  --arg image "${image_ref}" \
  --arg source_revision "${source_revision}" \
  --argjson previous "${previous_release}" \
  --arg manifest_sha256 "${manifest_sha256}" \
  --arg change_request_id "${change_request_id}" \
  --arg workflow_run_id "${workflow_run_id}" \
  --arg workflow_run_attempt "${workflow_run_attempt}" \
  --arg completed_at "${deployed_at}" \
  --argjson completed_at_epoch_ms "${deployed_at_epoch_ms}" \
  --arg completed_by "${started_by}" \
  '{
    schema_version: "1",
    event_id: $event_id,
    status: "success",
    operation: "deployment",
    target: {
      image: $image,
      source_revision: $source_revision
    },
    previous: $previous,
    authorization: {
      manifest_sha256: $manifest_sha256,
      change_request_id: $change_request_id,
      workflow_run_id: $workflow_run_id,
      workflow_run_attempt: $workflow_run_attempt
    },
    completed_at: $completed_at,
    completed_at_epoch_ms: $completed_at_epoch_ms,
    completed_by: $completed_by
  }' > "${release_event}"

bash "${ROOT_DIR}/scripts/manage-production-release-ledger.sh" \
  record-success "${RELEASE_LEDGER_DIR}" "${release_event}" >/dev/null

used_tmp="${used_record}.tmp"
jq \
  --arg status "used" \
  --arg deployed_at "${deployed_at}" \
  --arg release_event_id "${manifest_sha256}" \
  '.status = $status | .deployed_at = $deployed_at | .release_event_id = $release_event_id' \
  "${started_record}" > "${used_tmp}"
chmod 600 "${used_tmp}"
mv "${used_tmp}" "${used_record}"
rm -f "${started_record}"

cp "${used_record}" "${RESULT_DIR}/authorization-use-record.json"
cleanup_lock
trap - EXIT

echo "Attestation付き許可証による本番Composeデプロイとrelease台帳更新に成功しました: ${image_ref}"
