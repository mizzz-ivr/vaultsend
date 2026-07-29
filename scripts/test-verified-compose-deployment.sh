#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

FAKE_BIN="${TMP_DIR}/bin"
COMMAND_LOG="${TMP_DIR}/commands.log"
COMPOSE_UP_MARKER="${TMP_DIR}/compose-up"
ENV_FILE="${TMP_DIR}/operations.env"
mkdir -p "${FAKE_BIN}"
: > "${COMMAND_LOG}"
: > "${ENV_FILE}"

VALID_DIGEST="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
VALID_IMAGE="ghcr.io/mizzz-ivr/vaultsend@${VALID_DIGEST}"
VALID_REVISION="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

cat > "${FAKE_BIN}/docker" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'docker %s\n' "$*" >> "${MOCK_COMMAND_LOG}"

case "${1:-}" in
  pull)
    [[ "${MOCK_DOCKER_PULL_FAIL:-false}" != "true" ]]
    ;;
  image)
    [[ "${2:-}" == "inspect" ]] || exit 2
    if [[ "${3:-}" == "--format" ]]; then
      case "${4:-}" in
        *RepoDigests*)
          printf '["%s"]\n' "${MOCK_IMAGE_REF}"
          ;;
        *Labels*)
          printf '{"org.opencontainers.image.source":"%s","org.opencontainers.image.revision":"%s"}\n' \
            "${MOCK_SOURCE_URL:-https://github.com/mizzz-ivr/vaultsend}" \
            "${MOCK_REVISION:-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb}"
          ;;
        *)
          exit 2
          ;;
      esac
    else
      printf '[{"RepoDigests":["%s"],"Config":{"Labels":{"org.opencontainers.image.source":"%s","org.opencontainers.image.revision":"%s"}}}]\n' \
        "${MOCK_IMAGE_REF}" \
        "${MOCK_SOURCE_URL:-https://github.com/mizzz-ivr/vaultsend}" \
        "${MOCK_REVISION:-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb}"
    fi
    ;;
  compose)
    if [[ " $* " == *" up "* ]]; then
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
[[ "${MOCK_COSIGN_FAIL:-false}" != "true" ]]
printf '[{"critical":{"identity":{"docker-reference":"%s"}}}]\n' "${MOCK_IMAGE_REF}"
EOF

cat > "${FAKE_BIN}/gh" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'gh %s\n' "$*" >> "${MOCK_COMMAND_LOG}"

if [[ "${1:-}" == "auth" && "${2:-}" == "status" ]]; then
  [[ "${MOCK_GH_AUTH_FAIL:-false}" != "true" ]]
  exit 0
fi

if [[ "${1:-}" == "attestation" && "${2:-}" == "verify" ]]; then
  kind="provenance"
  if [[ " $* " == *" --predicate-type "* ]]; then
    kind="sbom"
  fi
  if [[ "${MOCK_GH_FAIL_KIND:-}" == "${kind}" ]]; then
    exit 1
  fi
  printf '[{"verificationResult":{"statement":{"predicateType":"%s"}}}]\n' "${kind}"
  exit 0
fi

exit 2
EOF

chmod +x "${FAKE_BIN}/docker" "${FAKE_BIN}/cosign" "${FAKE_BIN}/gh"

base_env=(
  PATH="${FAKE_BIN}:${PATH}"
  MOCK_COMMAND_LOG="${COMMAND_LOG}"
  MOCK_IMAGE_REF="${VALID_IMAGE}"
  MOCK_REVISION="${VALID_REVISION}"
  MOCK_COMPOSE_UP_MARKER="${COMPOSE_UP_MARKER}"
  VAULTSEND_COMPOSE_FILE="${ROOT_DIR}/deploy/compose/operations.yml"
  VAULTSEND_COMPOSE_ENV_FILE="${ENV_FILE}"
)

case_number=0

new_case() {
  case_number=$((case_number + 1))
  rm -f "${COMPOSE_UP_MARKER}"
  : > "${COMMAND_LOG}"
  export DEPLOY_VERIFICATION_DIR="${TMP_DIR}/report-${case_number}"
}

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

new_case
expect_failure "イメージ未指定を拒否" \
  env "${base_env[@]}" bash "${ROOT_DIR}/scripts/verify-release-image.sh"

new_case
expect_failure "タグのみを拒否" \
  env "${base_env[@]}" bash "${ROOT_DIR}/scripts/verify-release-image.sh" \
    ghcr.io/mizzz-ivr/vaultsend:main

new_case
expect_failure "別repositoryを拒否" \
  env "${base_env[@]}" bash "${ROOT_DIR}/scripts/verify-release-image.sh" \
    "ghcr.io/attacker/vaultsend@${VALID_DIGEST}"

new_case
expect_failure "不正digestを拒否" \
  env "${base_env[@]}" bash "${ROOT_DIR}/scripts/verify-release-image.sh" \
    ghcr.io/mizzz-ivr/vaultsend@sha256:1234

new_case
expect_failure "pull失敗時に拒否" \
  env "${base_env[@]}" MOCK_DOCKER_PULL_FAIL=true \
    bash "${ROOT_DIR}/scripts/verify-release-image.sh" "${VALID_IMAGE}"

new_case
expect_failure "source label不一致を拒否" \
  env "${base_env[@]}" MOCK_SOURCE_URL=https://github.com/attacker/vaultsend \
    bash "${ROOT_DIR}/scripts/verify-release-image.sh" "${VALID_IMAGE}"

new_case
expect_failure "revision label不正を拒否" \
  env "${base_env[@]}" MOCK_REVISION=not-a-commit \
    bash "${ROOT_DIR}/scripts/verify-release-image.sh" "${VALID_IMAGE}"

new_case
expect_failure "GitHub CLI未認証を拒否" \
  env "${base_env[@]}" MOCK_GH_AUTH_FAIL=true \
    bash "${ROOT_DIR}/scripts/verify-release-image.sh" "${VALID_IMAGE}"

new_case
expect_failure "Cosign署名失敗時に拒否" \
  env "${base_env[@]}" MOCK_COSIGN_FAIL=true \
    bash "${ROOT_DIR}/scripts/verify-release-image.sh" "${VALID_IMAGE}"

new_case
expect_failure "provenance失敗時に拒否" \
  env "${base_env[@]}" MOCK_GH_FAIL_KIND=provenance \
    bash "${ROOT_DIR}/scripts/verify-release-image.sh" "${VALID_IMAGE}"

new_case
expect_failure "SPDX SBOM失敗時に拒否" \
  env "${base_env[@]}" MOCK_GH_FAIL_KIND=sbom \
    bash "${ROOT_DIR}/scripts/verify-release-image.sh" "${VALID_IMAGE}"

new_case
expect_success "署名・provenance・SBOMの正常検証" \
  env "${base_env[@]}" bash "${ROOT_DIR}/scripts/verify-release-image.sh" "${VALID_IMAGE}"

jq -e \
  --arg image "${VALID_IMAGE}" \
  --arg revision "${VALID_REVISION}" \
  '.image == $image and .revision == $revision and
   .cosign_signature_verified == true and
   .provenance_verified == true and
   .spdx_sbom_verified == true' \
  "${DEPLOY_VERIFICATION_DIR}/verification-summary.json" >/dev/null
[[ "$(grep -c '^gh attestation verify ' "${COMMAND_LOG}")" -eq 2 ]]
[[ "$(grep -c '^cosign verify ' "${COMMAND_LOG}")" -eq 1 ]]

new_case
expect_success "--checkではComposeを起動しない" \
  env "${base_env[@]}" \
    bash "${ROOT_DIR}/scripts/deploy-verified-compose.sh" --check "${VALID_IMAGE}"
[[ ! -e "${COMPOSE_UP_MARKER}" ]]
grep -q '^docker compose .* config --quiet$' "${COMMAND_LOG}"

new_case
expect_success "--deployでは検証後にComposeを起動" \
  env "${base_env[@]}" \
    bash "${ROOT_DIR}/scripts/deploy-verified-compose.sh" --deploy "${VALID_IMAGE}"
[[ -e "${COMPOSE_UP_MARKER}" ]]
grep -q '^docker compose .* up -d --remove-orphans --no-build$' "${COMMAND_LOG}"

echo "検証済みComposeデプロイのmockテストに成功しました。"
