#!/usr/bin/env bash
set -Eeuo pipefail

umask 077

CHANGE_REQUEST_ID="${CHANGE_REQUEST_ID:-}"
ROLLBACK_REASON="${ROLLBACK_REASON:-}"
ROLLBACK_CONFIRMATION="${ROLLBACK_CONFIRMATION:-}"
EXPECTED_CURRENT_IMAGE="${EXPECTED_CURRENT_IMAGE:-}"
EXPECTED_CURRENT_REVISION="${EXPECTED_CURRENT_REVISION:-}"
TARGET_IMAGE="${TARGET_IMAGE:-}"
TARGET_REVISION="${TARGET_REVISION:-}"
REQUESTED_BY="${REQUESTED_BY:-}"
REQUESTED_REF="${REQUESTED_REF:-}"
REQUESTED_SHA="${REQUESTED_SHA:-}"
REQUEST_RUN_ID="${REQUEST_RUN_ID:-}"
REQUEST_RUN_ATTEMPT="${REQUEST_RUN_ATTEMPT:-}"
REQUEST_DIR="${PRODUCTION_ROLLBACK_REQUEST_DIR:-artifacts/production-rollback/request}"
EXPECTED_IMAGE_REPOSITORY="${EXPECTED_IMAGE_REPOSITORY:-ghcr.io/mizzz-ivr/vaultsend}"

fail() {
  echo "本番ロールバック要求エラー: $*" >&2
  exit 1
}

require_command() {
  local command_name="$1"
  command -v "${command_name}" >/dev/null 2>&1 \
    || fail "必要なコマンドが見つかりません: ${command_name}"
}

for command_name in date jq mkdir sha256sum; do
  require_command "${command_name}"
done

if [[ "${REQUESTED_REF}" != "refs/heads/main" ]]; then
  fail "本番ロールバックはmainブランチからのみ申請できます: ${REQUESTED_REF}"
fi
if [[ "${ROLLBACK_CONFIRMATION}" != "ROLLBACK_PRODUCTION" ]]; then
  fail "確認文字列が一致しません。ROLLBACK_PRODUCTIONを指定してください"
fi
if [[ ! "${CHANGE_REQUEST_ID}" =~ ^[A-Za-z0-9][A-Za-z0-9._:/-]{2,79}$ ]]; then
  fail "変更管理番号は3〜80文字の英数字・._:/-で指定してください"
fi
if [[ ${#ROLLBACK_REASON} -lt 10 || ${#ROLLBACK_REASON} -gt 300 ]]; then
  fail "ロールバック理由は10〜300文字で指定してください"
fi
if [[ "${ROLLBACK_REASON}" == *$'\n'* || "${ROLLBACK_REASON}" == *$'\r'* ]]; then
  fail "ロールバック理由に改行は使用できません"
fi
if [[ "${ROLLBACK_REASON}" =~ [[:cntrl:]] ]]; then
  fail "ロールバック理由に制御文字は使用できません"
fi
if [[ ! "${REQUESTED_BY}" =~ ^[A-Za-z0-9-]{1,39}$ ]]; then
  fail "起動者のGitHub login形式が不正です: ${REQUESTED_BY}"
fi
if [[ ! "${REQUESTED_SHA}" =~ ^[0-9a-f]{40}$ ]]; then
  fail "Workflow commitが40桁のcommit SHAではありません: ${REQUESTED_SHA}"
fi
if [[ ! "${REQUEST_RUN_ID}" =~ ^[0-9]+$ || ! "${REQUEST_RUN_ATTEMPT}" =~ ^[0-9]+$ ]]; then
  fail "Workflow run情報が不正です"
fi

for image_value in "${EXPECTED_CURRENT_IMAGE}" "${TARGET_IMAGE}"; do
  if [[ ! "${image_value}" =~ ^ghcr\.io/[a-z0-9._-]+/[a-z0-9._-]+@sha256:[0-9a-f]{64}$ ]]; then
    fail "ロールバック対象イメージはdigest固定で指定してください: ${image_value}"
  fi
  if [[ "${image_value%@*}" != "${EXPECTED_IMAGE_REPOSITORY}" ]]; then
    fail "許可されていないimage repositoryです: ${image_value%@*}"
  fi
done

if [[ ! "${EXPECTED_CURRENT_REVISION}" =~ ^[0-9a-f]{40}$ || ! "${TARGET_REVISION}" =~ ^[0-9a-f]{40}$ ]]; then
  fail "戻し元・戻し先revisionは40桁のcommit SHAで指定してください"
fi
if [[ "${EXPECTED_CURRENT_IMAGE}" == "${TARGET_IMAGE}" || "${EXPECTED_CURRENT_REVISION}" == "${TARGET_REVISION}" ]]; then
  fail "戻し元と戻し先は異なるreleaseを指定してください"
fi
if [[ -L "${REQUEST_DIR}" ]]; then
  fail "要求記録directoryにsymbolic linkは使用できません: ${REQUEST_DIR}"
fi

mkdir -p "${REQUEST_DIR}"
chmod 700 "${REQUEST_DIR}"

requested_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
reason_sha256="$(printf '%s' "${ROLLBACK_REASON}" | sha256sum | awk '{print $1}')"

jq -n \
  --arg change_request_id "${CHANGE_REQUEST_ID}" \
  --arg rollback_reason_sha256 "${reason_sha256}" \
  --arg requested_by "${REQUESTED_BY}" \
  --arg requested_ref "${REQUESTED_REF}" \
  --arg requested_sha "${REQUESTED_SHA}" \
  --arg run_id "${REQUEST_RUN_ID}" \
  --arg run_attempt "${REQUEST_RUN_ATTEMPT}" \
  --arg requested_at "${requested_at}" \
  --arg current_image "${EXPECTED_CURRENT_IMAGE}" \
  --arg current_revision "${EXPECTED_CURRENT_REVISION}" \
  --arg target_image "${TARGET_IMAGE}" \
  --arg target_revision "${TARGET_REVISION}" \
  '{
    change_request_id: $change_request_id,
    rollback_reason_sha256: $rollback_reason_sha256,
    requested_by: $requested_by,
    requested_ref: $requested_ref,
    requested_sha: $requested_sha,
    run_id: $run_id,
    run_attempt: $run_attempt,
    requested_at: $requested_at,
    expected_current: {
      image: $current_image,
      source_revision: $current_revision
    },
    target: {
      image: $target_image,
      source_revision: $target_revision
    },
    confirmation_verified: true
  }' > "${REQUEST_DIR}/request.json"

jq -e \
  '.confirmation_verified == true and
   .requested_ref == "refs/heads/main" and
   .expected_current.image != .target.image and
   .expected_current.source_revision != .target.source_revision' \
  "${REQUEST_DIR}/request.json" >/dev/null

echo "本番ロールバック要求の入力検証に成功しました: ${CHANGE_REQUEST_ID}"
