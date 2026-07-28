#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

IMAGE_REF="${SUPPLY_CHAIN_IMAGE_REF:-vaultsend:supply-chain-scan}"
TRIVY_IMAGE="${TRIVY_IMAGE:-aquasec/trivy:0.70.0}"
ARTIFACT_DIR="${SUPPLY_CHAIN_ARTIFACT_DIR:-artifacts/supply-chain}"
IMAGE_TAR="${ARTIFACT_DIR}/vaultsend-image.tar"
CACHE_DIR="${ROOT_DIR}/.trivy-cache"

mkdir -p "${ARTIFACT_DIR}" "${CACHE_DIR}"

if [[ "${SUPPLY_CHAIN_SKIP_BUILD:-false}" != "true" ]]; then
  docker build \
    --pull \
    --target runtime \
    --tag "${IMAGE_REF}" \
    .
fi

runtime_user="$(docker image inspect --format '{{.Config.User}}' "${IMAGE_REF}")"
if [[ "${runtime_user}" != "65532:65532" ]]; then
  echo "コンテナ実行ユーザーが非root固定ではありません: ${runtime_user}" >&2
  exit 1
fi
printf '%s\n' "${runtime_user}" > "${ARTIFACT_DIR}/runtime-user.txt"

docker pull "${TRIVY_IMAGE}"
docker image inspect --format '{{index .RepoDigests 0}}' "${TRIVY_IMAGE}" \
  | tee "${ARTIFACT_DIR}/trivy-image-digest.txt"

docker save "${IMAGE_REF}" --output "${IMAGE_TAR}"

run_trivy() {
  docker run --rm \
    --user "$(id -u):$(id -g)" \
    --env HOME=/tmp \
    --env TRIVY_CACHE_DIR=/workspace/.trivy-cache \
    --volume "${ROOT_DIR}:/workspace" \
    --workdir /workspace \
    "${TRIVY_IMAGE}" "$@"
}

run_trivy fs \
  --scanners secret,misconfig \
  --severity HIGH,CRITICAL \
  --exit-code 1 \
  --format table \
  --output "${ARTIFACT_DIR}/source-security-report.txt" \
  .

run_trivy image \
  --input "${IMAGE_TAR}" \
  --scanners vuln \
  --severity HIGH,CRITICAL \
  --format json \
  --output "${ARTIFACT_DIR}/vulnerabilities.json"

run_trivy image \
  --input "${IMAGE_TAR}" \
  --format cyclonedx \
  --output "${ARTIFACT_DIR}/vaultsend.cdx.json"

run_trivy image \
  --input "${IMAGE_TAR}" \
  --format spdx-json \
  --output "${ARTIFACT_DIR}/vaultsend.spdx.json"

jq -e '.bomFormat == "CycloneDX" and (.components | type == "array")' \
  "${ARTIFACT_DIR}/vaultsend.cdx.json" >/dev/null
jq -e '(.spdxVersion | startswith("SPDX-")) and (.packages | type == "array")' \
  "${ARTIFACT_DIR}/vaultsend.spdx.json" >/dev/null

# 修正版が提供されているHigh/Critical脆弱性はリリースを停止する。
# 修正版が存在しない脆弱性はJSONレポートへ残し、リスク受容または代替策を別途判断する。
run_trivy image \
  --input "${IMAGE_TAR}" \
  --scanners vuln \
  --severity HIGH,CRITICAL \
  --ignore-unfixed \
  --exit-code 1 \
  --format table \
  --output "${ARTIFACT_DIR}/vulnerability-gate.txt"

rm -f "${IMAGE_TAR}"
echo "SBOM生成・Secret／設定検査・脆弱性ゲートに成功しました。"
