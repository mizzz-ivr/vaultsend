#!/usr/bin/env bash
set -Eeuo pipefail

umask 077

CHANGE_REQUEST_ID="${CHANGE_REQUEST_ID:-}"
DEPLOYMENT_REASON="${DEPLOYMENT_REASON:-}"
DEPLOYMENT_CONFIRMATION="${DEPLOYMENT_CONFIRMATION:-}"
REQUESTED_BY="${REQUESTED_BY:-}"
REQUESTED_REF="${REQUESTED_REF:-}"
REQUESTED_SHA="${REQUESTED_SHA:-}"
REQUEST_RUN_ID="${REQUEST_RUN_ID:-}"
REQUEST_RUN_ATTEMPT="${REQUEST_RUN_ATTEMPT:-}"
REQUEST_DIR="${PRODUCTION_DEPLOYMENT_REQUEST_DIR:-artifacts/production-deployment/request}"

fail() {
  echo "本番デプロイ要求エラー: $*" >&2
  exit 1
}

require_command() {
  local command_name="$1"
  command -v "${command_name}" >/dev/null 2>&1 \
    || fail "必要なコマンドが見つかりません: ${command_name}"
}

for command_name in awk date jq sha256sum; do
  require_command "${command_name}"
done

if [[ "${REQUESTED_REF}" != "refs/heads/main" ]]; then
  fail "本番デプロイはmainブランチからのみ実行できます: ${REQUESTED_REF}"
fi
if [[ "${DEPLOYMENT_CONFIRMATION}" != "DEPLOY_PRODUCTION" ]]; then
  fail "確認文字列が一致しません。DEPLOY_PRODUCTIONを指定してください"
fi
if [[ ! "${CHANGE_REQUEST_ID}" =~ ^[A-Za-z0-9][A-Za-z0-9._:/-]{2,79}$ ]]; then
  fail "変更管理番号は3〜80文字の英数字・._:/-で指定してください"
fi
if [[ ${#DEPLOYMENT_REASON} -lt 10 || ${#DEPLOYMENT_REASON} -gt 300 ]]; then
  fail "デプロイ理由は10〜300文字で指定してください"
fi
if [[ "${DEPLOYMENT_REASON}" == *$'\n'* || "${DEPLOYMENT_REASON}" == *$'\r'* ]]; then
  fail "デプロイ理由に改行は使用できません"
fi
if [[ "${DEPLOYMENT_REASON}" =~ [[:cntrl:]] ]]; then
  fail "デプロイ理由に制御文字は使用できません"
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
if [[ -L "${REQUEST_DIR}" ]]; then
  fail "要求記録ディレクトリにsymbolic linkは使用できません: ${REQUEST_DIR}"
fi

mkdir -p "${REQUEST_DIR}"
chmod 700 "${REQUEST_DIR}"

requested_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
reason_sha256="$(printf '%s' "${DEPLOYMENT_REASON}" | sha256sum | awk '{print $1}')"

jq -n \
  --arg change_request_id "${CHANGE_REQUEST_ID}" \
  --arg deployment_reason "${DEPLOYMENT_REASON}" \
  --arg reason_sha256 "${reason_sha256}" \
  --arg requested_by "${REQUESTED_BY}" \
  --arg requested_ref "${REQUESTED_REF}" \
  --arg requested_sha "${REQUESTED_SHA}" \
  --arg run_id "${REQUEST_RUN_ID}" \
  --arg run_attempt "${REQUEST_RUN_ATTEMPT}" \
  --arg requested_at "${requested_at}" \
  '{
    change_request_id: $change_request_id,
    deployment_reason: $deployment_reason,
    deployment_reason_sha256: $reason_sha256,
    requested_by: $requested_by,
    requested_ref: $requested_ref,
    requested_sha: $requested_sha,
    run_id: $run_id,
    run_attempt: $run_attempt,
    requested_at: $requested_at,
    confirmation_verified: true
  }' > "${REQUEST_DIR}/request.json"

jq -e \
  '.confirmation_verified == true and .requested_ref == "refs/heads/main"' \
  "${REQUEST_DIR}/request.json" >/dev/null

echo "本番デプロイ要求の入力検証に成功しました: ${CHANGE_REQUEST_ID}"
