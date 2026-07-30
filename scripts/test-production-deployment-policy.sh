#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

FAKE_BIN="${TMP_DIR}/bin"
mkdir -p "${FAKE_BIN}"

VALID_REVISION="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
OTHER_REVISION="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
VALID_DIGEST="sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
VALID_IMAGE="ghcr.io/mizzz-ivr/vaultsend@${VALID_DIGEST}"

cat > "${FAKE_BIN}/docker" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail

case "${1:-}" in
  pull)
    [[ "${MOCK_DOCKER_PULL_FAIL:-false}" != "true" ]]
    ;;
  image)
    [[ "${2:-}" == "inspect" && "${3:-}" == "--format" ]] || exit 2
    case "${4:-}" in
      *RepoDigests*)
        printf '["%s"]\n' "${MOCK_IMAGE_REF}"
        ;;
      *Labels*)
        printf '{"org.opencontainers.image.source":"%s","org.opencontainers.image.revision":"%s"}\n' \
          "${MOCK_SOURCE_URL:-https://github.com/mizzz-ivr/vaultsend}" \
          "${MOCK_REVISION}"
        ;;
      *)
        exit 2
        ;;
    esac
    ;;
  *)
    exit 2
    ;;
esac
EOF

cat > "${FAKE_BIN}/git" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail

case "${1:-}" in
  fetch)
    [[ "${MOCK_GIT_FETCH_FAIL:-false}" != "true" ]]
    ;;
  rev-list)
    printf '%s\n' "${MOCK_EXPECTED_REVISION}"
    ;;
  *)
    exit 2
    ;;
esac
EOF

chmod +x "${FAKE_BIN}/docker" "${FAKE_BIN}/git"

expect_failure() {
  local name="$1"
  shift
  if "$@" >"${TMP_DIR}/stdout" 2>"${TMP_DIR}/stderr"; then
    echo "FAIL: ${name}: 失敗を期待しましたが成功しました" >&2
    exit 1
  fi
  echo "PASS: ${name}"
}

expect_success() {
  local name="$1"
  shift
  if ! "$@" >"${TMP_DIR}/stdout" 2>"${TMP_DIR}/stderr"; then
    echo "FAIL: ${name}: 成功を期待しましたが失敗しました" >&2
    cat "${TMP_DIR}/stderr" >&2
    exit 1
  fi
  echo "PASS: ${name}"
}

resolver_env=(
  PATH="${FAKE_BIN}:${PATH}"
  MOCK_IMAGE_REF="${VALID_IMAGE}"
  MOCK_REVISION="${VALID_REVISION}"
  MOCK_EXPECTED_REVISION="${VALID_REVISION}"
)

resolution_dir="${TMP_DIR}/resolution-success"
expect_success "mainタグをdigestと公開commitへ解決" \
  env "${resolver_env[@]}" RELEASE_RESOLUTION_DIR="${resolution_dir}" \
    bash "${ROOT_DIR}/scripts/resolve-current-release-image.sh"

jq -e \
  --arg image "${VALID_IMAGE}" \
  --arg revision "${VALID_REVISION}" \
  '.image == $image and .revision == $revision and
   .expected_revision == $revision and .digest != ""' \
  "${resolution_dir}/release-resolution.json" >/dev/null

expect_failure "期待commitとOCI revisionの不一致を拒否" \
  env "${resolver_env[@]}" EXPECTED_SOURCE_REVISION="${OTHER_REVISION}" \
    RELEASE_RESOLUTION_DIR="${TMP_DIR}/resolution-mismatch" \
    bash "${ROOT_DIR}/scripts/resolve-current-release-image.sh"

expect_failure "mainタグのpull失敗を拒否" \
  env "${resolver_env[@]}" MOCK_DOCKER_PULL_FAIL=true \
    RELEASE_RESOLUTION_DIR="${TMP_DIR}/resolution-pull-failure" \
    bash "${ROOT_DIR}/scripts/resolve-current-release-image.sh"

request_env=(
  CHANGE_REQUEST_ID="CHG-2026-0001"
  DEPLOYMENT_REASON="監査workerの検証済みリリースを本番へ反映する"
  DEPLOYMENT_CONFIRMATION="DEPLOY_PRODUCTION"
  REQUESTED_BY="mizzz-ivr"
  REQUESTED_REF="refs/heads/main"
  REQUESTED_SHA="dddddddddddddddddddddddddddddddddddddddd"
  REQUEST_RUN_ID="123456"
  REQUEST_RUN_ATTEMPT="1"
)

request_dir="${TMP_DIR}/request-success"
expect_success "本番デプロイ要求の正常入力" \
  env "${request_env[@]}" PRODUCTION_DEPLOYMENT_REQUEST_DIR="${request_dir}" \
    bash "${ROOT_DIR}/scripts/validate-production-deployment-request.sh"

jq -e \
  '.change_request_id == "CHG-2026-0001" and
   .requested_ref == "refs/heads/main" and
   .confirmation_verified == true and
   (.deployment_reason_sha256 | length) == 64' \
  "${request_dir}/request.json" >/dev/null

expect_failure "main以外の起動を拒否" \
  env "${request_env[@]}" REQUESTED_REF="refs/heads/feature/test" \
    PRODUCTION_DEPLOYMENT_REQUEST_DIR="${TMP_DIR}/request-branch" \
    bash "${ROOT_DIR}/scripts/validate-production-deployment-request.sh"

expect_failure "確認文字列不一致を拒否" \
  env "${request_env[@]}" DEPLOYMENT_CONFIRMATION="deploy" \
    PRODUCTION_DEPLOYMENT_REQUEST_DIR="${TMP_DIR}/request-confirmation" \
    bash "${ROOT_DIR}/scripts/validate-production-deployment-request.sh"

expect_failure "不正な変更管理番号を拒否" \
  env "${request_env[@]}" CHANGE_REQUEST_ID="NG ID" \
    PRODUCTION_DEPLOYMENT_REQUEST_DIR="${TMP_DIR}/request-change-id" \
    bash "${ROOT_DIR}/scripts/validate-production-deployment-request.sh"

expect_failure "短すぎるデプロイ理由を拒否" \
  env "${request_env[@]}" DEPLOYMENT_REASON="短い理由" \
    PRODUCTION_DEPLOYMENT_REQUEST_DIR="${TMP_DIR}/request-short-reason" \
    bash "${ROOT_DIR}/scripts/validate-production-deployment-request.sh"

expect_failure "改行を含むデプロイ理由を拒否" \
  env "${request_env[@]}" DEPLOYMENT_REASON=$'本番デプロイ理由です\n二行目' \
    PRODUCTION_DEPLOYMENT_REQUEST_DIR="${TMP_DIR}/request-newline" \
    bash "${ROOT_DIR}/scripts/validate-production-deployment-request.sh"

mkdir -p "${TMP_DIR}/real-request-dir"
ln -s "${TMP_DIR}/real-request-dir" "${TMP_DIR}/request-link"
expect_failure "要求記録先のsymbolic linkを拒否" \
  env "${request_env[@]}" PRODUCTION_DEPLOYMENT_REQUEST_DIR="${TMP_DIR}/request-link" \
    bash "${ROOT_DIR}/scripts/validate-production-deployment-request.sh"

workflow_file="${ROOT_DIR}/.github/workflows/production-deploy.yml"
acceptance_workflow="${ROOT_DIR}/.github/workflows/release-image-acceptance.yml"

[[ -f "${workflow_file}" ]]
grep -q '^  workflow_dispatch:' "${workflow_file}"
if grep -Eq '^  (pull_request|push|schedule|workflow_run):' "${workflow_file}"; then
  echo 'FAIL: 本番デプロイWorkflowにworkflow_dispatch以外のeventがあります' >&2
  exit 1
fi

inputs_block="$(awk '/^    inputs:/{capture=1; next} /^permissions:/{capture=0} capture' "${workflow_file}")"
if grep -Eiq '^[[:space:]]*(image|image_ref|digest|source_revision|revision):' <<<"${inputs_block}"; then
  echo 'FAIL: 本番デプロイWorkflowが任意イメージ入力を受け付けています' >&2
  exit 1
fi

if grep -Eq '^[[:space:]]+[A-Za-z_-]+:[[:space:]]+write([[:space:]]|$)' "${workflow_file}"; then
  echo 'FAIL: 本番デプロイWorkflowにwrite権限があります' >&2
  exit 1
fi

grep -Fq 'group: production-deployment' "${workflow_file}"
grep -Fq 'cancel-in-progress: false' "${workflow_file}"
grep -Fq 'name: production' "${workflow_file}"
grep -Fq 'runs-on: [self-hosted, linux, x64, vaultsend-production]' "${workflow_file}"
grep -Fq 'DEPLOY_PRODUCTION' "${workflow_file}"
grep -Fq 'scripts/resolve-current-release-image.sh' "${workflow_file}"
grep -Fq 'scripts/verify-release-image.sh' "${workflow_file}"
grep -Fq 'scripts/deploy-verified-compose.sh --deploy' "${workflow_file}"
grep -Fq 'EXPECTED_SOURCE_REVISION:' "${workflow_file}"
grep -Fq 'runner.environment' "${workflow_file}"
grep -Fq 'id -u' "${workflow_file}"
grep -Fq 'persist-credentials: false' "${workflow_file}"

grep -Fq 'scripts/resolve-current-release-image.sh' "${acceptance_workflow}"

echo 'PASS: 本番デプロイWorkflowのevent・権限・Environment・runner・digest固定policy'
echo '本番デプロイ承認policyのmockテストに成功しました。'
