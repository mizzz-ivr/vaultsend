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
  pull) [[ "${MOCK_DOCKER_PULL_FAIL:-false}" != "true" ]] ;;
  image)
    [[ "${2:-}" == "inspect" && "${3:-}" == "--format" ]] || exit 2
    case "${4:-}" in
      *RepoDigests*) printf '["%s"]\n' "${MOCK_IMAGE_REF}" ;;
      *Labels*) printf '{"org.opencontainers.image.source":"https://github.com/mizzz-ivr/vaultsend","org.opencontainers.image.revision":"%s"}\n' "${MOCK_REVISION}" ;;
      *) exit 2 ;;
    esac
    ;;
  *) exit 2 ;;
esac
EOF

cat > "${FAKE_BIN}/git" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
case "${1:-}" in
  fetch) exit 0 ;;
  rev-list) printf '%s\n' "${MOCK_EXPECTED_REVISION}" ;;
  *) exit 2 ;;
esac
EOF
chmod +x "${FAKE_BIN}/docker" "${FAKE_BIN}/git"

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

base_env=(
  PATH="${FAKE_BIN}:${PATH}"
  MOCK_IMAGE_REF="${VALID_IMAGE}"
  MOCK_REVISION="${VALID_REVISION}"
  MOCK_EXPECTED_REVISION="${VALID_REVISION}"
)

resolution_dir="${TMP_DIR}/resolution"
expect_success "mainタグをdigestと公開commitへ解決" \
  env "${base_env[@]}" RELEASE_RESOLUTION_DIR="${resolution_dir}" \
    bash "${ROOT_DIR}/scripts/resolve-current-release-image.sh"
jq -e --arg image "${VALID_IMAGE}" --arg revision "${VALID_REVISION}" \
  '.image == $image and .revision == $revision and .expected_revision == $revision' \
  "${resolution_dir}/release-resolution.json" >/dev/null

expect_failure "期待commitとの不一致を拒否" \
  env "${base_env[@]}" EXPECTED_SOURCE_REVISION="${OTHER_REVISION}" \
    RELEASE_RESOLUTION_DIR="${TMP_DIR}/mismatch" \
    bash "${ROOT_DIR}/scripts/resolve-current-release-image.sh"

expect_failure "mainタグのpull失敗を拒否" \
  env "${base_env[@]}" MOCK_DOCKER_PULL_FAIL=true \
    RELEASE_RESOLUTION_DIR="${TMP_DIR}/pull-failure" \
    bash "${ROOT_DIR}/scripts/resolve-current-release-image.sh"

request_env=(
  CHANGE_REQUEST_ID="CHG-2026-0001"
  DEPLOYMENT_REASON="検証済みリリースを本番へ反映するため"
  DEPLOYMENT_CONFIRMATION="DEPLOY_PRODUCTION"
  REQUESTED_BY="mizzz-ivr"
  REQUESTED_REF="refs/heads/main"
  REQUESTED_SHA="dddddddddddddddddddddddddddddddddddddddd"
  REQUEST_RUN_ID="123456"
  REQUEST_RUN_ATTEMPT="1"
)

request_dir="${TMP_DIR}/request"
expect_success "本番デプロイ要求の正常入力" \
  env "${request_env[@]}" PRODUCTION_DEPLOYMENT_REQUEST_DIR="${request_dir}" \
    bash "${ROOT_DIR}/scripts/validate-production-deployment-request.sh"
jq -e '.confirmation_verified == true and .requested_ref == "refs/heads/main"' \
  "${request_dir}/request.json" >/dev/null

expect_failure "main以外の起動を拒否" \
  env "${request_env[@]}" REQUESTED_REF="refs/heads/feature/test" \
    PRODUCTION_DEPLOYMENT_REQUEST_DIR="${TMP_DIR}/branch" \
    bash "${ROOT_DIR}/scripts/validate-production-deployment-request.sh"
expect_failure "確認文字列不一致を拒否" \
  env "${request_env[@]}" DEPLOYMENT_CONFIRMATION="deploy" \
    PRODUCTION_DEPLOYMENT_REQUEST_DIR="${TMP_DIR}/confirmation" \
    bash "${ROOT_DIR}/scripts/validate-production-deployment-request.sh"
expect_failure "不正な変更管理番号を拒否" \
  env "${request_env[@]}" CHANGE_REQUEST_ID="NG ID" \
    PRODUCTION_DEPLOYMENT_REQUEST_DIR="${TMP_DIR}/change-id" \
    bash "${ROOT_DIR}/scripts/validate-production-deployment-request.sh"
expect_failure "改行を含む理由を拒否" \
  env "${request_env[@]}" DEPLOYMENT_REASON=$'本番デプロイ理由です\n二行目' \
    PRODUCTION_DEPLOYMENT_REQUEST_DIR="${TMP_DIR}/newline" \
    bash "${ROOT_DIR}/scripts/validate-production-deployment-request.sh"

workflow_file="${ROOT_DIR}/.github/workflows/production-deploy.yml"
acceptance_workflow="${ROOT_DIR}/.github/workflows/release-image-acceptance.yml"

[[ -f "${workflow_file}" ]]
grep -q '^  workflow_dispatch:' "${workflow_file}"
if grep -Eq '^  (pull_request|push|schedule|workflow_run):' "${workflow_file}"; then
  echo 'FAIL: workflow_dispatch以外のeventがあります' >&2
  exit 1
fi
inputs_block="$(awk '/^    inputs:/{capture=1; next} /^permissions:/{capture=0} capture' "${workflow_file}")"
if grep -Eiq '^[[:space:]]*(image|image_ref|digest|source_revision|revision):' <<<"${inputs_block}"; then
  echo 'FAIL: 任意イメージ入力を受け付けています' >&2
  exit 1
fi
if grep -Fq 'self-hosted' "${workflow_file}"; then
  echo 'FAIL: 本番hostをself-hosted runnerとして使用しています' >&2
  exit 1
fi
if grep -Eq '^[[:space:]]+(contents|packages|deployments):[[:space:]]+write([[:space:]]|$)' "${workflow_file}"; then
  echo 'FAIL: 不要なwrite権限があります' >&2
  exit 1
fi

for pattern in \
  'group: production-deployment-authorization' \
  'cancel-in-progress: false' \
  'name: production' \
  'PRODUCTION_DEPLOYMENT_ENABLED' \
  'PRODUCTION_ENVIRONMENT_GUARD' \
  'actions/attest@59d89421af93a897026c735860bf21b6eb4f7b26' \
  'subject-path:' \
  'artifact-metadata: write' \
  'id-token: write' \
  'attestations: write' \
  'authorized_at_epoch + 7200' \
  'gh attestation verify' \
  'persist-credentials: false'; do
  grep -Fq "${pattern}" "${workflow_file}"
done

grep -Fq 'scripts/resolve-current-release-image.sh' "${acceptance_workflow}"
echo 'PASS: Environment・Attestation・digest固定policy'
echo '本番デプロイ許可Workflowのpolicyテストに成功しました。'
