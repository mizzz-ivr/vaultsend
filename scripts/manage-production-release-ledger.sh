#!/usr/bin/env bash
set -Eeuo pipefail

umask 077

COMMAND="${1:-}"
LEDGER_DIR="${2:-${PRODUCTION_RELEASE_LEDGER_DIR:-}}"
ARG3="${3:-}"
ARG4="${4:-}"

usage() {
  cat <<'EOF'
Usage:
  bash scripts/manage-production-release-ledger.sh validate <ledger-dir>
  bash scripts/manage-production-release-ledger.sh current <ledger-dir>
  bash scripts/manage-production-release-ledger.sh assert-target <ledger-dir> <image> <source-revision>
  bash scripts/manage-production-release-ledger.sh record-success <ledger-dir> <event-json>
EOF
}

fail() {
  echo "本番release台帳エラー: $*" >&2
  exit 1
}

require_command() {
  local command_name="$1"
  command -v "${command_name}" >/dev/null 2>&1 \
    || fail "必要なコマンドが見つかりません: ${command_name}"
}

for command_name in chmod cp find jq mkdir mktemp mv readlink; do
  require_command "${command_name}"
done

if [[ -z "${COMMAND}" || -z "${LEDGER_DIR}" ]]; then
  usage >&2
  fail "commandとrelease台帳directoryを指定してください"
fi
if [[ -L "${LEDGER_DIR}" ]]; then
  fail "release台帳directoryにsymbolic linkは使用できません: ${LEDGER_DIR}"
fi

EVENTS_DIR="${LEDGER_DIR}/events"
CURRENT_FILE="${LEDGER_DIR}/current-release.json"

initialize_ledger() {
  mkdir -p "${LEDGER_DIR}" "${EVENTS_DIR}"
  chmod 700 "${LEDGER_DIR}" "${EVENTS_DIR}"
  [[ ! -L "${EVENTS_DIR}" ]] || fail "events directoryにsymbolic linkは使用できません"
  [[ ! -L "${CURRENT_FILE}" ]] || fail "current releaseにsymbolic linkは使用できません"
}

validate_release_object() {
  local file="$1"
  jq -e '
    .schema_version == "1" and
    .status == "success" and
    (.operation == "deployment" or .operation == "rollback") and
    (.event_id | type == "string" and test("^[0-9a-f]{64}$")) and
    (.target.image | type == "string" and test("^ghcr\\.io/[a-z0-9._-]+/[a-z0-9._-]+@sha256:[0-9a-f]{64}$")) and
    (.target.source_revision | type == "string" and test("^[0-9a-f]{40}$")) and
    (.authorization.manifest_sha256 | type == "string" and test("^[0-9a-f]{64}$")) and
    (.authorization.change_request_id | type == "string" and test("^[A-Za-z0-9][A-Za-z0-9._:/-]{2,79}$")) and
    (.authorization.workflow_run_id | type == "string" and test("^[0-9]+$")) and
    (.authorization.workflow_run_attempt | type == "string" and test("^[0-9]+$")) and
    (.completed_at | type == "string" and length > 0) and
    (.completed_at_epoch_ms | type == "number" and . > 0) and
    (.completed_by | type == "string" and length > 0) and
    (
      .previous == null or
      (
        (.previous.event_id | type == "string" and test("^[0-9a-f]{64}$")) and
        (.previous.image | type == "string" and test("^ghcr\\.io/[a-z0-9._-]+/[a-z0-9._-]+@sha256:[0-9a-f]{64}$")) and
        (.previous.source_revision | type == "string" and test("^[0-9a-f]{40}$"))
      )
    )
  ' "${file}" >/dev/null || fail "release eventの形式が不正です: ${file}"
}

validate_ledger() {
  initialize_ledger

  local event_files=()
  while IFS= read -r -d '' event_file; do
    [[ ! -L "${event_file}" ]] || fail "event fileにsymbolic linkは使用できません: ${event_file}"
    validate_release_object "${event_file}"
    event_files+=("${event_file}")
  done < <(find "${EVENTS_DIR}" -maxdepth 1 -type f -name '*.json' -print0 | sort -z)

  if [[ ! -e "${CURRENT_FILE}" ]]; then
    [[ ${#event_files[@]} -eq 0 ]] || fail "event履歴があるのにcurrent releaseがありません"
    return 0
  fi

  [[ -f "${CURRENT_FILE}" ]] || fail "current releaseが通常ファイルではありません"
  validate_release_object "${CURRENT_FILE}"
  [[ ${#event_files[@]} -gt 0 ]] || fail "current releaseがあるのにevent履歴がありません"

  local latest_event_id current_event_id
  latest_event_id="$({
    for event_file in "${event_files[@]}"; do
      jq -c '{event_id, completed_at_epoch_ms}' "${event_file}"
    done
  } | jq -sr 'sort_by(.completed_at_epoch_ms, .event_id) | last | .event_id')"
  current_event_id="$(jq -r '.event_id' "${CURRENT_FILE}")"
  [[ "${current_event_id}" == "${latest_event_id}" ]] \
    || fail "current releaseと最新eventが一致しません: current=${current_event_id} latest=${latest_event_id}"

  local latest_file="${EVENTS_DIR}/${latest_event_id}.json"
  [[ -f "${latest_file}" && ! -L "${latest_file}" ]] || fail "current releaseに対応するeventがありません"
  jq -e --slurpfile current "${CURRENT_FILE}" '. == $current[0]' "${latest_file}" >/dev/null \
    || fail "current releaseと対応eventの内容が一致しません"
}

case "${COMMAND}" in
  validate)
    validate_ledger
    echo "本番release台帳の整合性検証に成功しました"
    ;;

  current)
    validate_ledger
    [[ -f "${CURRENT_FILE}" ]] || fail "current releaseが未登録です。先に通常の承認済みデプロイを実行してください"
    cat "${CURRENT_FILE}"
    ;;

  assert-target)
    target_image="${ARG3}"
    target_revision="${ARG4}"
    [[ -n "${target_image}" && -n "${target_revision}" ]] || fail "target imageとsource revisionを指定してください"
    validate_ledger

    matched='false'
    while IFS= read -r -d '' event_file; do
      if jq -e \
        --arg image "${target_image}" \
        --arg revision "${target_revision}" \
        '.status == "success" and .target.image == $image and .target.source_revision == $revision' \
        "${event_file}" >/dev/null; then
        matched='true'
        break
      fi
    done < <(find "${EVENTS_DIR}" -maxdepth 1 -type f -name '*.json' -print0 | sort -z)

    [[ "${matched}" == "true" ]] \
      || fail "rollback対象はこのhostで正常デプロイされた履歴に存在しません"
    echo "rollback対象の正常デプロイ履歴を確認しました"
    ;;

  record-success)
    event_file="${ARG3}"
    [[ -n "${event_file}" && -f "${event_file}" && ! -L "${event_file}" ]] \
      || fail "記録するrelease event JSONを通常ファイルで指定してください"
    validate_ledger
    validate_release_object "${event_file}"

    event_id="$(jq -r '.event_id' "${event_file}")"
    operation="$(jq -r '.operation' "${event_file}")"
    destination="${EVENTS_DIR}/${event_id}.json"
    [[ ! -e "${destination}" ]] || fail "同じevent IDは既に記録されています: ${event_id}"

    if [[ -f "${CURRENT_FILE}" ]]; then
      jq -e --slurpfile current "${CURRENT_FILE}" '
        .previous != null and
        .previous.event_id == $current[0].event_id and
        .previous.image == $current[0].target.image and
        .previous.source_revision == $current[0].target.source_revision
      ' "${event_file}" >/dev/null \
        || fail "eventのprevious releaseがcurrent releaseと一致しません"
    else
      [[ "${operation}" == "deployment" ]] || fail "current release未登録時は通常デプロイで初期化してください"
      jq -e '.previous == null' "${event_file}" >/dev/null \
        || fail "初回デプロイeventのpreviousはnullである必要があります"
    fi

    tmp_event="$(mktemp "${EVENTS_DIR}/.${event_id}.event.XXXXXX")"
    tmp_current="$(mktemp "${LEDGER_DIR}/.current-release.XXXXXX")"
    cp "${event_file}" "${tmp_event}"
    cp "${event_file}" "${tmp_current}"
    chmod 600 "${tmp_event}" "${tmp_current}"

    mv "${tmp_event}" "${destination}"
    mv "${tmp_current}" "${CURRENT_FILE}"
    validate_ledger
    echo "本番release台帳へ成功eventを記録しました: ${event_id}"
    ;;

  -h|--help)
    usage
    ;;

  *)
    usage >&2
    fail "不明なcommandです: ${COMMAND}"
    ;;
esac
