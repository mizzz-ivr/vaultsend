#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TEMP_DIR}"' EXIT

VAULTSEND_IMAGE="${VAULTSEND_IMAGE:-vaultsend:operations-check}"
PROMETHEUS_IMAGE="${PROMETHEUS_IMAGE:-prom/prometheus:v3.13.1}"
ALERTMANAGER_IMAGE="${ALERTMANAGER_IMAGE:-prom/alertmanager:v0.32.1}"
GRAFANA_IMAGE="${GRAFANA_IMAGE:-grafana/grafana:13.1.0}"

printf '%s\n' 'postgres://vaultsend:vaultsend@postgres:5432/vaultsend?sslmode=require' > "${TEMP_DIR}/audit-worker-database-url"
printf '%s\n' '01234567890123456789012345678901' > "${TEMP_DIR}/internal-metrics-token"
printf '%s\n' 'https://alerts.example.invalid/vaultsend' > "${TEMP_DIR}/alertmanager-webhook-url"
printf '%s\n' 'replace-this-grafana-password-in-production' > "${TEMP_DIR}/grafana-admin-password"

chmod 600 "${TEMP_DIR}"/*

echo 'VaultSendコンテナイメージをビルドします'
docker build --target runtime -t "${VAULTSEND_IMAGE}" "${ROOT_DIR}"

container_user="$(docker image inspect --format '{{.Config.User}}' "${VAULTSEND_IMAGE}")"
if [[ "${container_user}" != '65532:65532' ]]; then
  echo "コンテナ実行ユーザーが非root固定ではありません: ${container_user}" >&2
  exit 1
fi

echo 'Compose設定を検証します'
VAULTSEND_IMAGE="${VAULTSEND_IMAGE}" \
PROMETHEUS_IMAGE="${PROMETHEUS_IMAGE}" \
ALERTMANAGER_IMAGE="${ALERTMANAGER_IMAGE}" \
GRAFANA_IMAGE="${GRAFANA_IMAGE}" \
AUDIT_WORKER_DATABASE_URL_FILE="${TEMP_DIR}/audit-worker-database-url" \
INTERNAL_METRICS_TOKEN_FILE="${TEMP_DIR}/internal-metrics-token" \
ALERTMANAGER_WEBHOOK_URL_FILE="${TEMP_DIR}/alertmanager-webhook-url" \
GRAFANA_ADMIN_PASSWORD_FILE="${TEMP_DIR}/grafana-admin-password" \
docker compose \
  -f "${ROOT_DIR}/deploy/compose/operations.yml" \
  config > "${TEMP_DIR}/operations.rendered.yml"

echo 'Prometheus設定とAlertルールを検証します'
docker run --rm \
  --entrypoint promtool \
  -v "${ROOT_DIR}/deploy/monitoring/prometheus:/etc/prometheus:ro" \
  -v "${TEMP_DIR}/internal-metrics-token:/run/secrets/internal_metrics_token:ro" \
  "${PROMETHEUS_IMAGE}" \
  check config /etc/prometheus/prometheus.yml

docker run --rm \
  --entrypoint promtool \
  -v "${ROOT_DIR}/deploy/monitoring/prometheus/alerts:/etc/prometheus/alerts:ro" \
  "${PROMETHEUS_IMAGE}" \
  check rules /etc/prometheus/alerts/vaultsend-audit-outbox.yml

echo 'Alertmanager設定を検証します'
docker run --rm \
  --entrypoint amtool \
  -v "${ROOT_DIR}/deploy/monitoring/alertmanager:/etc/alertmanager:ro" \
  -v "${TEMP_DIR}/alertmanager-webhook-url:/run/secrets/alertmanager_webhook_url:ro" \
  "${ALERTMANAGER_IMAGE}" \
  check-config /etc/alertmanager/alertmanager.yml

echo 'GrafanaダッシュボードJSONを検証します'
jq -e . "${ROOT_DIR}/deploy/monitoring/grafana/dashboards/vaultsend-audit-outbox.json" >/dev/null

echo 'コンテナ・Compose・監視設定の検証に成功しました'
