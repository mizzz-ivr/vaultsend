#!/usr/bin/env bash
set -Eeuo pipefail

umask 077

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODE="${1:---check}"
MANIFEST_PATH="${2:-${PRODUCTION_ROLLBACK_MANIFEST:-}}"
EXPECTED_GITHUB_REPOSITORY="${EXPECTED_GITHUB_REPOSITORY:-mizzz-ivr/vaultsend}"
EXPECTED_IMAGE_REPOSITORY="${EXPECTED_IMAGE_REPOSITORY:-ghcr.io/mizzz-ivr/vaultsend}"
EXPECTED_SIGNER_WORKFLOW="${EXPECTED_ROLLBACK_SIGNER_WORKFLOW:-mizzz-ivr/vaultsend/.github/workflows/production-rollback.yml}"
EXPECTED_WORKFLOW_REF="${EXPECTED_ROLLBACK_WORKFLOW_REF:-mizzz-ivr/vaultsend/.github/workflows/production-rollback.yml@refs/heads/main}"
EXPECTED_SOURCE_REF="${EXPECTED_ROLLBACK_SOURCE_REF:-refs/heads/main}"
EXPECTED_OIDC_ISSUER="${EXPECTED_OIDC_ISSUER:-https://token.actions.githubusercontent.com}"
RESULT_DIR="${PRODUCTION_ROLLBACK_RESULT_DIR:-artifacts/production-rollback/execution}"
RELEASE_LEDGER_DIR="${PRODUCTION_RELEASE_LEDGER_DIR:-}"
AUTHORIZATION_LEDGER_DIR="${PRODUCTION_ROLLBACK_AUTHORIZATION_LEDGER_DIR:-${PRODUCTION_AUTHORIZATION_LEDGER_DIR:+${PRODUCTION_AUTHORIZATION_LEDGER_DIR}/rollbacks}}"
MAX_AUTHORIZATION_TTL_SEC="${PRODUCTION_ROLLBACK_MAX_TTL_SEC:-1800}"
COMPOSE_FILE="${VAULTSEND_COMPOSE_FILE:-${ROOT_DIR}/deploy/compose/operations.yml}"
COMPOSE_ENV_FILE="${VAULTSEND_COMPOSE_ENV_FILE:-${ROOT_DIR}/deploy/compose/.env}"

usage() {
  cat <<'EOF'
Usage:
  PRODUCTION_RELEASE_LEDGER_DIR=/var/lib/vaultsend/releases \
    bash scripts/rollback-approved-production.sh --check \
      /secure/path/rollback-authorization-manifest.json

  PRODUCTION_RELEASE_LEDGER_DIR=/var/lib/vaultsend/releases \
  PRODUCTION_ROLLBACK_AUTHORIZATION_LEDGER_DIR=/var/lib/vaultsend/rollback-authorizations \
    bash scripts/rollback-approved-production.sh --deploy \
      /secure/path/rollback-authorization-manifest.json

Modes:
  --check   許可証・現在release・過去の成功履歴・イメージ・Compose設定を検証する。起動しない。
  --deploy  上記を再検証し、未使用の許可証で一度だけロールバックする。
EOF
}

fail() {
  echo "承認済み本番ロールバックエラー: $*" >&2
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
  fail "ロールバック許可manifestを指定してください"
fi
if [[ ! -f "${MANIFEST_PATH}" || -L "${MANIFEST_PATH}" ]]; then
  fail "許可manifestが通常ファイルではありません: ${MANIFEST_PATH}"
fi
if [[ -z "${RELEASE_LEDGER_DIR}" ]]; then
  fail "PRODUCTION_RELEASE_LEDGER_DIRを指定してください"
fi
if [[ ! "${MAX_AUTHORIZATION_TTL_SEC}" =~ ^[0-9]+$ || "${MAX_AUTHORIZATION_TTL_SEC}" -lt 300 || "${MAX_AUTHORIZATION_TTL_SEC}" -gt 3600 ]]; then
  fail "ロールバック許可証の最大TTL設定が不正です: ${MAX_AUTHORIZATION_TTL_SEC}"
fi

for command_name in awk cosign cp date docker flock gh git grep id jq mkdir mv rm rmdir sha256sum sort; do
  require_command "${command_name}"
done

for path_value in "${RESULT_DIR}" "${RELEASE_LEDGER_DIR}"; do
  [[ ! -L "${path_value}" ]] || fail "directoryにsymbolic linkは使用できません: ${path_value}"
done
mkdir -p "${RESULT_DIR}" "${RELEASE_LEDGER_DIR}"
chmod 700 "${RESULT_DIR}" "${RELEASE_LEDGER_DIR}"

manifest_abs="$(cd "$(dirname "${MANIFEST_PATH}")" && pwd)/$(basename "${MANIFEST_PATH}")"
manifest_sha256="$(sha256sum "${manifest_abs}" | awk '{print $1}')"
jq -e . "${manifest_abs}" >/dev/null || fail "許可manifestがJSONではありません"

schema_version="$(jq -er '.schema_version // empty' "${manifest_abs}")" || fail "schema_versionを取得できません"
authorization_type="$(jq -er '.authorization_type // empty' "${manifest_abs}")" || fail "authorization_typeを取得できません"
authorization_status="$(jq -er '.authorization_status // empty' "${manifest_abs}")" || fail "authorization_statusを取得できません"
environment_name="$(jq -er '.environment // empty' "${manifest_abs}")" || fail "environmentを取得できません"
current_image="$(jq -er '.expected_current.image // empty' "${manifest_abs}")" || fail "戻し元imageを取得できません"
current_revision="$(jq -er '.expected_current.source_revision // empty' "${manifest_abs}")" || fail "戻し元revisionを取得できません"
target_image="$(jq -er '.target.image // empty' "${manifest_abs}")" || fail "戻し先imageを取得できません"
target_revision="$(jq -er '.target.source_revision // empty' "${manifest_abs}")" || fail "戻し先revisionを取得できません"
change_request_id="$(jq -er '.change_request_id // empty' "${manifest_abs}")" || fail "変更管理番号を取得できません"
reason_sha256="$(jq -er '.rollback_reason_sha256 // empty' "${manifest_abs}")" || fail "理由digestを取得できません"
workflow_repository="$(jq -er '.workflow.repository // empty' "${manifest_abs}")" || fail "workflow repositoryを取得できません"
workflow_ref="$(jq -er '.workflow.ref // empty' "${manifest_abs}")" || fail "workflow refを取得できません"
workflow_sha="$(jq -er '.workflow.sha // empty' "${manifest_abs}")" || fail "workflow SHAを取得できません"
workflow_run_id="$(jq -er '.workflow.run_id // empty' "${manifest_abs}")" || fail "workflow run IDを取得できません"
workflow_run_attempt="$(jq -er '.workflow.run_attempt // empty' "${manifest_abs}")" || fail "workflow run attemptを取得できません"
authorized_at_epoch="$(jq -er '.authorized_at_epoch // empty' "${manifest_abs}")" || fail "authorized_at_epochを取得できません"
expires_at_epoch="$(jq -er '.expires_at_epoch // empty' "${manifest_abs}")" || fail "expires_at_epochを取得できません"

[[ "${schema_version}" == "1" ]] || fail "未対応の許可manifest schemaです: ${schema_version}"
[[ "${authorization_type}" == "rollback" ]] || fail "許可証種別がrollbackではありません"
[[ "${authorization_status}" == "approved" ]] || fail "許可状態がapprovedではありません"
[[ "${environment_name}" == "production-rollback" ]] || fail "許可環境がproduction-rollbackではありません"
[[ "${workflow_repository}" == "${EXPECTED_GITHUB_REPOSITORY}" ]] || fail "workflow repositoryが期待値と一致しません"
[[ "${workflow_ref}" == "${EXPECTED_WORKFLOW_REF}" ]] || fail "workflow refが期待値と一致しません"

for image_value in "${current_image}" "${target_image}"; do
  [[ "${image_value}" =~ ^ghcr\.io/[a-z0-9._-]+/[a-z0-9._-]+@sha256:[0-9a-f]{64}$ ]] \
    || fail "許可イメージ形式が不正です: ${image_value}"
  [[ "${image_value%@*}" == "${EXPECTED_IMAGE_REPOSITORY}" ]] \
    || fail "許可イメージrepositoryが期待値と一致しません: ${image_value%@*}"
done

[[ "${current_revision}" =~ ^[0-9a-f]{40}$ && "${target_revision}" =~ ^[0-9a-f]{40}$ ]] \
  || fail "source revision形式が不正です"
[[ "${current_image}" != "${target_image}" && "${current_revision}" != "${target_revision}" ]] \
  || fail "戻し元と戻し先は異なるreleaseである必要があります"
[[ "${workflow_sha}" =~ ^[0-9a-f]{40}$ ]] || fail "workflow SHA形式が不正です"
[[ "${workflow_run_id}" =~ ^[0-9]+$ && "${workflow_run_attempt}" =~ ^[0-9]+$ ]] || fail "Workflow run情報が不正です"
[[ "${change_request_id}" =~ ^[A-Za-z0-9][A-Za-z0-9._:/-]{2,79}$ ]] || fail "変更管理番号形式が不正です"
[[ "${reason_sha256}" =~ ^[0-9a-f]{64}$ ]] || fail "理由digest形式が不正です"
[[ "${authorized_at_epoch}" =~ ^[0-9]+$ && "${expires_at_epoch}" =~ ^[0-9]+$ ]] || fail "許可時刻形式が不正です"

now_epoch="$(date -u +%s)"
authorization_ttl=$((expires_at_epoch - authorized_at_epoch))
(( authorization_ttl > 0 && authorization_ttl <= MAX_AUTHORIZATION_TTL_SEC )) \
  || fail "ロールバック許可証TTLが許容範囲外です: ${authorization_ttl}秒"
(( now_epoch >= authorized_at_epoch - 300 )) || fail "許可開始時刻が未来です"
(( now_epoch <= expires_at_epoch )) || fail "ロールバック許可証が失効しています"

if ! gh auth status >/dev/null 2>&1; then
  fail "GitHub CLIが認証されていません。gh auth loginまたはGH_TOKENを設定してください"
fi

echo "ロールバック許可manifestのArtifact Attestationを検証します"
gh attestation verify "${manifest_abs}" \
  --repo "${EXPECTED_GITHUB_REPOSITORY}" \
  --signer-workflow "${EXPECTED_SIGNER_WORKFLOW}" \
  --source-ref "${EXPECTED_SOURCE_REF}" \
  --source-digest "${workflow_sha}" \
  --cert-oidc-issuer "${EXPECTED_OIDC_ISSUER}" \
  --deny-self-hosted-runners \
  --format json > "${RESULT_DIR}/authorization-attestation-verification.json"

jq -e 'type == "array" and length > 0' \
  "${RESULT_DIR}/authorization-attestation-verification.json" >/dev/null \
  || fail "ロールバック許可manifestのAttestation検証結果が空です"

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
   (.path == ".github/workflows/production-rollback.yml" or
    .path == "mizzz-ivr/vaultsend/.github/workflows/production-rollback.yml@refs/heads/main")' \
  "${RESULT_DIR}/authorization-workflow-run.json" >/dev/null \
  || fail "ロールバック許可証のWorkflow runが期待条件を満たしません"

bash "${ROOT_DIR}/scripts/manage-production-release-ledger.sh" validate "${RELEASE_LEDGER_DIR}" >/dev/null
current_file="${RELEASE_LEDGER_DIR}/current-release.json"
[[ -f "${current_file}" && ! -L "${current_file}" ]] \
  || fail "current releaseが未登録です。先に通常の承認済みデプロイで台帳を初期化してください"

jq -e \
  --arg image "${current_image}" \
  --arg revision "${current_revision}" \
  '.target.image == $image and .target.source_revision == $revision' \
  "${current_file}" >/dev/null \
  || fail "本番hostのcurrent releaseが許可証の戻し元と一致しません"

bash "${ROOT_DIR}/scripts/manage-production-release-ledger.sh" \
  assert-target "${RELEASE_LEDGER_DIR}" "${target_image}" "${target_revision}" >/dev/null

git fetch --no-tags origin main:refs/remotes/origin/main
first_parent_history="$(git rev-list --first-parent origin/main)"
grep -Fxq "${current_revision}" <<<"${first_parent_history}" \
  || fail "戻し元revisionがmainのfirst-parent履歴にありません"
grep -Fxq "${target_revision}" <<<"${first_parent_history}" \
  || fail "戻し先revisionがmainのfirst-parent履歴にありません"
git merge-base --is-ancestor "${target_revision}" "${current_revision}" \
  || fail "戻し先revisionは戻し元revisionの祖先ではありません"

compose_command=(docker compose --env-file "${COMPOSE_ENV_FILE}" -f "${COMPOSE_FILE}")
container_id="$(VAULTSEND_IMAGE="${current_image}" "${compose_command[@]}" ps -q audit-worker)"
[[ -n "${container_id}" ]] || fail "稼働中のaudit-worker containerを確認できません"
running_image="$(docker inspect --format '{{.Config.Image}}' "${container_id}")"
[[ "${running_image}" == "${current_image}" ]] \
  || fail "実際に稼働中のaudit-worker imageがcurrent releaseと一致しません: ${running_image}"

export VAULTSEND_IMAGE="${current_image}"
export EXPECTED_SOURCE_REVISION="${current_revision}"
export DEPLOY_VERIFICATION_DIR="${RESULT_DIR}/current-release-verification"
bash "${ROOT_DIR}/scripts/verify-release-image.sh"

export VAULTSEND_IMAGE="${target_image}"
export EXPECTED_SOURCE_REVISION="${target_revision}"
export DEPLOY_VERIFICATION_DIR="${RESULT_DIR}/target-release-verification"
bash "${ROOT_DIR}/scripts/deploy-verified-compose.sh" --check "${target_image}"

cp "${manifest_abs}" "${RESULT_DIR}/rollback-authorization-manifest.json"
printf '%s\n' "${manifest_sha256}" > "${RESULT_DIR}/rollback-authorization-manifest.sha256"

if [[ "${MODE}" == "--check" ]]; then
  echo "承認済み本番ロールバックの事前確認に成功しました。--checkのため起動していません。"
  exit 0
fi

if [[ -z "${AUTHORIZATION_LEDGER_DIR}" ]]; then
  fail "--deployではPRODUCTION_ROLLBACK_AUTHORIZATION_LEDGER_DIRを指定してください"
fi
[[ ! -L "${AUTHORIZATION_LEDGER_DIR}" ]] || fail "ロールバック許可証台帳にsymbolic linkは使用できません"
mkdir -p "${AUTHORIZATION_LEDGER_DIR}"
chmod 700 "${AUTHORIZATION_LEDGER_DIR}"

release_lock_file="${RELEASE_LEDGER_DIR}/deployment.lock"
[[ ! -L "${release_lock_file}" ]] || fail "release台帳lockにsymbolic linkは使用できません"
exec 9>"${release_lock_file}"
chmod 600 "${release_lock_file}"
flock -n 9 || fail "別の本番デプロイまたはrollbackが実行中です"

bash "${ROOT_DIR}/scripts/manage-production-release-ledger.sh" validate "${RELEASE_LEDGER_DIR}" >/dev/null
jq -e \
  --arg image "${current_image}" \
  --arg revision "${current_revision}" \
  '.target.image == $image and .target.source_revision == $revision' \
  "${current_file}" >/dev/null \
  || fail "ロック取得後にcurrent releaseが変更されました"
bash "${ROOT_DIR}/scripts/manage-production-release-ledger.sh" \
  assert-target "${RELEASE_LEDGER_DIR}" "${target_image}" "${target_revision}" >/dev/null

container_id="$(VAULTSEND_IMAGE="${current_image}" "${compose_command[@]}" ps -q audit-worker)"
[[ -n "${container_id}" ]] || fail "ロック取得後にaudit-worker containerを確認できません"
running_image="$(docker inspect --format '{{.Config.Image}}' "${container_id}")"
[[ "${running_image}" == "${current_image}" ]] || fail "ロック取得後に稼働imageが変更されました"

started_record="${AUTHORIZATION_LEDGER_DIR}/${manifest_sha256}.started.json"
used_record="${AUTHORIZATION_LEDGER_DIR}/${manifest_sha256}.used.json"
lock_dir="${AUTHORIZATION_LEDGER_DIR}/${manifest_sha256}.lock"
[[ ! -e "${started_record}" && ! -e "${used_record}" ]] \
  || fail "このロールバック許可証は使用済み、または過去に使用開始されています"
mkdir "${lock_dir}" 2>/dev/null || fail "このロールバック許可証は別処理で使用中です"
cleanup_lock() {
  rmdir "${lock_dir}" 2>/dev/null || true
}
trap cleanup_lock EXIT

started_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
started_by="$(id -un)"
jq -n \
  --arg status "started" \
  --arg manifest_sha256 "${manifest_sha256}" \
  --arg current_image "${current_image}" \
  --arg current_revision "${current_revision}" \
  --arg target_image "${target_image}" \
  --arg target_revision "${target_revision}" \
  --arg change_request_id "${change_request_id}" \
  --arg workflow_run_id "${workflow_run_id}" \
  --arg workflow_run_attempt "${workflow_run_attempt}" \
  --arg started_by "${started_by}" \
  --arg started_at "${started_at}" \
  '{
    status: $status,
    authorization_manifest_sha256: $manifest_sha256,
    expected_current: {image: $current_image, source_revision: $current_revision},
    target: {image: $target_image, source_revision: $target_revision},
    change_request_id: $change_request_id,
    workflow_run_id: $workflow_run_id,
    workflow_run_attempt: $workflow_run_attempt,
    started_by: $started_by,
    started_at: $started_at
  }' > "${started_record}"
chmod 600 "${started_record}"

export VAULTSEND_IMAGE="${target_image}"
export EXPECTED_SOURCE_REVISION="${target_revision}"
export DEPLOY_VERIFICATION_DIR="${RESULT_DIR}/deploy-verification"
bash "${ROOT_DIR}/scripts/deploy-verified-compose.sh" --deploy "${target_image}"

completed_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
completed_at_epoch_ms="$(date -u +%s%3N)"
previous_release="$(jq -c '{event_id, image: .target.image, source_revision: .target.source_revision}' "${current_file}")"
release_event="${RESULT_DIR}/release-event.json"
jq -n \
  --arg event_id "${manifest_sha256}" \
  --arg target_image "${target_image}" \
  --arg target_revision "${target_revision}" \
  --argjson previous "${previous_release}" \
  --arg manifest_sha256 "${manifest_sha256}" \
  --arg change_request_id "${change_request_id}" \
  --arg workflow_run_id "${workflow_run_id}" \
  --arg workflow_run_attempt "${workflow_run_attempt}" \
  --arg completed_at "${completed_at}" \
  --argjson completed_at_epoch_ms "${completed_at_epoch_ms}" \
  --arg completed_by "${started_by}" \
  '{
    schema_version: "1",
    event_id: $event_id,
    status: "success",
    operation: "rollback",
    target: {image: $target_image, source_revision: $target_revision},
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
  --arg completed_at "${completed_at}" \
  --arg release_event_id "${manifest_sha256}" \
  '.status = $status | .completed_at = $completed_at | .release_event_id = $release_event_id' \
  "${started_record}" > "${used_tmp}"
chmod 600 "${used_tmp}"
mv "${used_tmp}" "${used_record}"
rm -f "${started_record}"

cp "${used_record}" "${RESULT_DIR}/rollback-authorization-use-record.json"
cleanup_lock
trap - EXIT

echo "承認済みの過去releaseへの本番ロールバックに成功しました: ${target_image}"
