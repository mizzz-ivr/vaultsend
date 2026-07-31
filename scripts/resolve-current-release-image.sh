#!/usr/bin/env bash
set -Eeuo pipefail

umask 077

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EXPECTED_IMAGE_REPOSITORY="${EXPECTED_IMAGE_REPOSITORY:-ghcr.io/mizzz-ivr/vaultsend}"
EXPECTED_SOURCE_URL="${EXPECTED_SOURCE_URL:-https://github.com/mizzz-ivr/vaultsend}"
EXPECTED_SOURCE_REVISION="${EXPECTED_SOURCE_REVISION:-}"
RESOLUTION_DIR="${RELEASE_RESOLUTION_DIR:-artifacts/release-resolution}"

fail() {
  echo "公開リリース解決エラー: $*" >&2
  exit 1
}

require_command() {
  local command_name="$1"
  command -v "${command_name}" >/dev/null 2>&1 \
    || fail "必要なコマンドが見つかりません: ${command_name}"
}

for command_name in docker git jq date; do
  require_command "${command_name}"
done

if [[ ! "${EXPECTED_IMAGE_REPOSITORY}" =~ ^ghcr\.io/[a-z0-9._-]+/[a-z0-9._-]+$ ]]; then
  fail "期待するGHCR repository形式が不正です: ${EXPECTED_IMAGE_REPOSITORY}"
fi
if [[ -n "${EXPECTED_SOURCE_REVISION}" && ! "${EXPECTED_SOURCE_REVISION}" =~ ^[0-9a-f]{40}$ ]]; then
  fail "期待するsource revisionが40桁のcommit SHAではありません: ${EXPECTED_SOURCE_REVISION}"
fi
if [[ -L "${RESOLUTION_DIR}" ]]; then
  fail "解決結果ディレクトリにsymbolic linkは使用できません: ${RESOLUTION_DIR}"
fi

mkdir -p "${RESOLUTION_DIR}"
chmod 700 "${RESOLUTION_DIR}"

cd "${ROOT_DIR}"

image_tag="${EXPECTED_IMAGE_REPOSITORY}:main"
echo "公開済みmainタグを取得します: ${image_tag}"
docker pull "${image_tag}"

repo_digests_json="$(docker image inspect --format '{{json .RepoDigests}}' "${image_tag}")"
image_ref="$(
  jq -er \
    --arg prefix "${EXPECTED_IMAGE_REPOSITORY}@" \
    '[.[] | select(startswith($prefix))][0] // empty' \
    <<<"${repo_digests_json}"
)" || fail "mainタグのRepoDigestを取得できません"

if [[ ! "${image_ref}" =~ ^ghcr\.io/[a-z0-9._-]+/[a-z0-9._-]+@sha256:[0-9a-f]{64}$ ]]; then
  fail "mainタグを正規のdigest参照へ解決できませんでした: ${image_ref}"
fi
if [[ "${image_ref%@*}" != "${EXPECTED_IMAGE_REPOSITORY}" ]]; then
  fail "解決したイメージrepositoryが期待値と一致しません: ${image_ref%@*}"
fi

image_digest="${image_ref#*@}"
labels_json="$(docker image inspect --format '{{json .Config.Labels}}' "${image_ref}")"
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

if [[ -n "${EXPECTED_SOURCE_REVISION}" ]]; then
  expected_revision="${EXPECTED_SOURCE_REVISION}"
else
  git fetch --no-tags origin main:refs/remotes/origin/main
  expected_revision="$(
    git rev-list --first-parent -1 origin/main -- \
      Dockerfile \
      .dockerignore \
      .gitignore \
      go.mod \
      go.sum \
      cmd \
      internal \
      web/package.json \
      web/package-lock.json \
      scripts/verify-supply-chain.sh \
      .github/workflows/supply-chain.yml \
      .github/dependabot.yml
  )"
fi

if [[ ! "${expected_revision}" =~ ^[0-9a-f]{40}$ ]]; then
  fail "期待する公開対象commitを解決できませんでした: ${expected_revision}"
fi
if [[ "${revision}" != "${expected_revision}" ]]; then
  fail "OCI revisionが期待する公開対象commitと一致しません: expected=${expected_revision} actual=${revision}"
fi

resolved_at="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"

printf '%s\n' "${image_ref}" > "${RESOLUTION_DIR}/image-reference.txt"
printf '%s\n' "${expected_revision}" > "${RESOLUTION_DIR}/expected-revision.txt"

jq -n \
  --arg image "${image_ref}" \
  --arg repository "${EXPECTED_IMAGE_REPOSITORY}" \
  --arg tag "${image_tag}" \
  --arg digest "${image_digest}" \
  --arg source "${source_url}" \
  --arg revision "${revision}" \
  --arg expected_revision "${expected_revision}" \
  --arg resolved_at "${resolved_at}" \
  '{
    image: $image,
    repository: $repository,
    tag: $tag,
    digest: $digest,
    source: $source,
    revision: $revision,
    expected_revision: $expected_revision,
    resolved_at: $resolved_at
  }' > "${RESOLUTION_DIR}/release-resolution.json"

jq -e \
  '.image != "" and .digest != "" and .revision == .expected_revision' \
  "${RESOLUTION_DIR}/release-resolution.json" >/dev/null

echo "公開済みmainイメージをdigestへ解決しました: ${image_ref}"
echo "公開対象commit: ${expected_revision}"
