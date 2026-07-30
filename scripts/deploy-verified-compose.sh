#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="${VAULTSEND_COMPOSE_FILE:-${ROOT_DIR}/deploy/compose/operations.yml}"
ENV_FILE="${VAULTSEND_COMPOSE_ENV_FILE:-${ROOT_DIR}/deploy/compose/.env}"
MODE="${1:---check}"
IMAGE_REF="${2:-${VAULTSEND_IMAGE:-}}"

usage() {
  cat <<'EOF'
Usage:
  bash scripts/deploy-verified-compose.sh --check \
    ghcr.io/mizzz-ivr/vaultsend@sha256:<64桁のdigest>

  bash scripts/deploy-verified-compose.sh --deploy \
    ghcr.io/mizzz-ivr/vaultsend@sha256:<64桁のdigest>

Modes:
  --check   署名・AttestationとCompose設定を検証する。コンテナは起動しない。
  --deploy  検証成功後にComposeを起動する。
EOF
}

fail() {
  echo "検証済みComposeデプロイエラー: $*" >&2
  exit 1
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

if [[ -z "${IMAGE_REF}" ]]; then
  usage >&2
  fail "digest固定イメージを指定してください"
fi
if [[ ! -f "${COMPOSE_FILE}" ]]; then
  fail "Composeファイルが見つかりません: ${COMPOSE_FILE}"
fi
if [[ ! -f "${ENV_FILE}" ]]; then
  fail "Compose環境変数ファイルが見つかりません: ${ENV_FILE}"
fi

cd "${ROOT_DIR}"

bash scripts/verify-release-image.sh "${IMAGE_REF}"

compose_command=(
  docker compose
  --env-file "${ENV_FILE}"
  -f "${COMPOSE_FILE}"
)

echo "Compose設定を検証します"
VAULTSEND_IMAGE="${IMAGE_REF}" "${compose_command[@]}" config --quiet

if [[ "${MODE}" == "--check" ]]; then
  echo "デプロイ前検証に成功しました。--checkのためコンテナは起動していません。"
  exit 0
fi

echo "検証済みdigestでComposeを起動します: ${IMAGE_REF}"
VAULTSEND_IMAGE="${IMAGE_REF}" "${compose_command[@]}" up -d --remove-orphans --no-build
VAULTSEND_IMAGE="${IMAGE_REF}" "${compose_command[@]}" ps

echo "検証済みdigestのComposeデプロイに成功しました。"
