#!/usr/bin/env bash
set -Eeuo pipefail

umask 077

EXPECTED_IMAGE_REPOSITORY="${EXPECTED_IMAGE_REPOSITORY:-ghcr.io/mizzz-ivr/vaultsend}"
EXPECTED_GITHUB_REPOSITORY="${EXPECTED_GITHUB_REPOSITORY:-mizzz-ivr/vaultsend}"
EXPECTED_SOURCE_URL="${EXPECTED_SOURCE_URL:-https://github.com/mizzz-ivr/vaultsend}"
EXPECTED_SIGNER_WORKFLOW="${EXPECTED_SIGNER_WORKFLOW:-mizzz-ivr/vaultsend/.github/workflows/supply-chain.yml}"
EXPECTED_CERTIFICATE_IDENTITY="${EXPECTED_CERTIFICATE_IDENTITY:-https://github.com/mizzz-ivr/vaultsend/.github/workflows/supply-chain.yml@refs/heads/main}"
EXPECTED_SOURCE_REF="${EXPECTED_SOURCE_REF:-refs/heads/main}"
EXPECTED_SOURCE_REVISION="${EXPECTED_SOURCE_REVISION:-}"
EXPECTED_OIDC_ISSUER="${EXPECTED_OIDC_ISSUER:-https://token.actions.githubusercontent.com}"
SPDX_PREDICATE_TYPE="${SPDX_PREDICATE_TYPE:-https://spdx.dev/Document/v2.3}"
REPORT_DIR="${DEPLOY_VERIFICATION_DIR:-artifacts/deploy-verification}"
IMAGE_REF="${1:-${VAULTSEND_IMAGE:-}}"

usage() {
  cat <<'EOF'
Usage:
  EXPECTED_SOURCE_REVISION=<40桁のmain commit SHA> \
    bash scripts/verify-release-image.sh \
      ghcr.io/mizzz-ivr/vaultsend@sha256:<64桁のdigest>

Prerequisites:
  - docker login ghcr.io
  - gh auth login または GH_TOKEN
  - docker, cosign, gh, jq
EOF
}

fail() {
  echo "リリースイメージ検証エラー: $*" >&2
  exit 1
}

require_command() {
  local command_name="$1"
  command -v "${command_name}" >/dev/null 2>&1 \
    || fail "必要なコマンドが見つかりません: ${command_name}"
}

if [[ -z "${IMAGE_REF}" ]]; then
  usage >&2
  fail "検証対象イメージを指定してください"
fi

if [[ "${IMAGE_REF}" != *@* ]]; then
  fail "タグだけのイメージ参照は使用できません。digest固定で指定してください: ${IMAGE_REF}"
fi

image_repository="${IMAGE_REF%@*}"
image_digest="${IMAGE_REF#*@}"

if [[ "${image_repository}" != "${EXPECTED_IMAGE_REPOSITORY}" ]]; then
  fail "許可されていないイメージrepositoryです: ${image_repository}"
fi

if [[ ! "${image_digest}" =~ ^sha256:[0-9a-f]{64}$ ]]; then
  fail "SHA-256 digest形式が不正です: ${image_digest}"
fi

if [[ -n "${EXPECTED_SOURCE_REVISION}" && ! "${EXPECTED_SOURCE_REVISION}" =~ ^[0-9a-f]{40}$ ]]; then
  fail "期待するsource revisionが40桁のcommit SHAではありません: ${EXPECTED_SOURCE_REVISION}"
fi

for command_name in docker cosign gh jq; do
  require_command "${command_name}"
done

if [[ -L "${REPORT_DIR}" ]]; then
  fail "検証結果ディレクトリにsymbolic linkは使用できません: ${REPORT_DIR}"
fi
mkdir -p "${REPORT_DIR}"
chmod 700 "${REPORT_DIR}"

printf '%s\n' "${IMAGE_REF}" > "${REPORT_DIR}/requested-image.txt"

if [[ -n "${EXPECTED_SOURCE_REVISION}" ]]; then
  printf '%s\n' "${EXPECTED_SOURCE_REVISION}" > "${REPORT_DIR}/expected-source-revision.txt"
fi

echo "GHCRからdigest固定イメージを取得します: ${IMAGE_REF}"
docker pull "${IMAGE_REF}"

docker image inspect "${IMAGE_REF}" > "${REPORT_DIR}/image-inspect.json"
repo_digests_json="$(docker image inspect --format '{{json .RepoDigests}}' "${IMAGE_REF}")"
labels_json="$(docker image inspect --format '{{json .Config.Labels}}' "${IMAGE_REF}")"

jq -e --arg expected "${IMAGE_REF}" 'index($expected) != null' \
  <<<"${repo_digests_json}" >/dev/null \
  || fail "取得したイメージのRepoDigestが要求値と一致しません"

source_url="$(jq -er '."org.opencontainers.image.source" // empty' <<<"${labels_json}")" \
  || fail "OCI source labelを取得できません"
revision="$(jq -er '."org.opencontainers.image.revision" // empty' <<<"${labels_json}")" \
  || fail "OCI revision labelを取得できません"

if [[ "${source_url}" != "${EXPECTED_SOURCE_URL}" ]]; then
  fail "OCI source labelが期待値と一致しません: ${source_url}"
fi
if [[ ! "${revision}" =~ ^[0-9a-f]{40}$ ]]; then
  fail "OCI revision labelが40桁のcommit SHAではありません: ${revision}"
fi
if [[ -n "${EXPECTED_SOURCE_REVISION}" && "${revision}" != "${EXPECTED_SOURCE_REVISION}" ]]; then
  fail "OCI revision labelが期待するsource commitと一致しません: expected=${EXPECTED_SOURCE_REVISION} actual=${revision}"
fi

verified_source_revision="${EXPECTED_SOURCE_REVISION:-${revision}}"
printf '%s\n' "${revision}" > "${REPORT_DIR}/source-revision.txt"

if ! gh auth status >/dev/null 2>&1; then
  fail "GitHub CLIが認証されていません。gh auth loginまたはGH_TOKENを設定してください"
fi

echo "Cosign keyless署名を検証します"
cosign verify \
  --certificate-identity "${EXPECTED_CERTIFICATE_IDENTITY}" \
  --certificate-oidc-issuer "${EXPECTED_OIDC_ISSUER}" \
  --output json \
  "${IMAGE_REF}" > "${REPORT_DIR}/cosign-verification.json"

jq -e 'type == "array" and length > 0' \
  "${REPORT_DIR}/cosign-verification.json" >/dev/null \
  || fail "Cosign署名の検証結果が空です"

# GitHub CLIではsigner-workflowとcert-identityが排他的なため、
# Attestationはsigner workflow、source ref/digest、OIDC issuerで固定する。
# 完全なcertificate identityは上記Cosign検証で別途確認する。
attestation_common_args=(
  "oci://${IMAGE_REF}"
  --repo "${EXPECTED_GITHUB_REPOSITORY}"
  --signer-workflow "${EXPECTED_SIGNER_WORKFLOW}"
  --source-ref "${EXPECTED_SOURCE_REF}"
  --source-digest "${verified_source_revision}"
  --cert-oidc-issuer "${EXPECTED_OIDC_ISSUER}"
  --deny-self-hosted-runners
  --format json
)

echo "GitHub build provenance Attestationを検証します"
gh attestation verify "${attestation_common_args[@]}" \
  > "${REPORT_DIR}/provenance-verification.json"

jq -e 'type == "array" and length > 0' \
  "${REPORT_DIR}/provenance-verification.json" >/dev/null \
  || fail "build provenance Attestationの検証結果が空です"

echo "GitHub SPDX SBOM Attestationを検証します"
gh attestation verify "${attestation_common_args[@]}" \
  --predicate-type "${SPDX_PREDICATE_TYPE}" \
  > "${REPORT_DIR}/sbom-verification.json"

jq -e 'type == "array" and length > 0' \
  "${REPORT_DIR}/sbom-verification.json" >/dev/null \
  || fail "SPDX SBOM Attestationの検証結果が空です"

cat > "${REPORT_DIR}/verification-summary.json" <<EOF
{
  "image": "${IMAGE_REF}",
  "repository": "${EXPECTED_IMAGE_REPOSITORY}",
  "digest": "${image_digest}",
  "source": "${source_url}",
  "revision": "${revision}",
  "expected_revision": "${verified_source_revision}",
  "source_ref": "${EXPECTED_SOURCE_REF}",
  "attestation_signer_workflow": "${EXPECTED_SIGNER_WORKFLOW}",
  "cosign_certificate_identity": "${EXPECTED_CERTIFICATE_IDENTITY}",
  "oidc_issuer": "${EXPECTED_OIDC_ISSUER}",
  "provenance_verified": true,
  "spdx_sbom_verified": true,
  "cosign_signature_verified": true
}
EOF

jq -e . "${REPORT_DIR}/verification-summary.json" >/dev/null

echo "リリースイメージの署名・provenance・SBOM検証に成功しました: ${IMAGE_REF}"
