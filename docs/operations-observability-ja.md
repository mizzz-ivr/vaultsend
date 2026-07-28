# audit-worker・監視基盤のデプロイ／運用

## 1. 目的

監査Outboxを最終監査テーブルへ継続配送し、配送停止・遅延・監視不能をPrometheus、Alertmanager、Grafanaから検知できる運用構成を提供します。

本構成はクラウド固有のIaCを導入する前段として、以下を再利用可能な形で固定します。

- API・worker共通のコンテナイメージ
- audit-workerの独立プロセス起動
- Docker Secretによるworker用DB接続情報の注入
- Prometheus scrape・alert rule
- Alertmanager webhook通知
- Grafana datasource・dashboard provisioning
- CIによる設定検証

## 2. 適用範囲

`deploy/compose/operations.yml`は、単一ホストまたは検証環境での運用リファレンスです。

本番でAWS ECS、Kubernetes等を採用する場合も、次の責務と設定を移植します。

- APIとaudit-workerを別プロセス／別サービスとして起動する
- worker用DB資格情報をAPI用資格情報と分離する
- `/internal/metrics`は内部ネットワークからのみ到達可能にする
- Prometheus ruleとGrafana dashboardを同一内容で配備する
- Secretを平文環境変数、Git、コンテナイメージへ格納しない

## 3. コンテナイメージ

ルート`Dockerfile`は次の4バイナリを1つのイメージへ格納します。

- `/usr/local/bin/vaultsend-api`
- `/usr/local/bin/vaultsend-mail-worker`
- `/usr/local/bin/vaultsend-cleanup-worker`
- `/usr/local/bin/vaultsend-audit-worker`

既定コマンドはAPIです。workerはデプロイ定義の`command`で実行バイナリを切り替えます。

### セキュリティ特性

- multi-stage build
- `CGO_ENABLED=0`
- `-trimpath`
- debug symbol・build IDを除外
- distroless runtime
- UID/GID `65532:65532`の非root実行
- shell・package managerをruntime imageへ含めない

ビルド:

```bash
make container-build
```

本番ではCIで生成した単一digestをAPI・各workerで共有し、実行コマンドだけを変えます。タグだけでなくdigestを固定し、同一リリース内でバイナリ差異が発生しないようにします。

## 4. 使用バージョン

初期検証値は以下です。

- Prometheus `v3.13.1`
- Alertmanager `v0.32.1`
- Grafana `13.1.0`

Composeの環境変数で上書きできます。本番反映時は脆弱性スキャン済みのdigestへ置き換えます。

バージョン更新は一度に1コンポーネントずつ行い、設定検証・scrape・alert firing・dashboard表示を確認します。

## 5. 必要なSecret

Git管理外のファイルとして次を用意します。

| Secret | 内容 |
|---|---|
| `audit-worker-database-url` | worker専用DB roleのPostgreSQL接続文字列 |
| `internal-metrics-token` | `/internal/metrics`用32文字以上のランダム値 |
| `alertmanager-webhook-url` | 通知先のHTTPS webhook URL |
| `grafana-admin-password` | Grafana初期管理者パスワード |

推奨権限:

```bash
chmod 600 deploy/compose/secrets/*
```

### workerのDB Secret

audit-workerは以下のどちらか一方を受け付けます。

- `DATABASE_URL`
- `DATABASE_URL_FILE`

両方を指定した場合は起動を拒否します。Compose構成は`DATABASE_URL_FILE=/run/secrets/audit_worker_database_url`を利用します。

Secretファイルは最大64KiB、空ファイルは拒否します。

## 6. DB role

本番ではaudit-worker専用roleを使用します。

必要な権限:

- `security_audit_outbox`の`SELECT`
- `security_audit_outbox`の`UPDATE`
- 処理済み行に限定した`DELETE`
- `security_audit_events`の`INSERT`

付与しない権限:

- shipment、file、user、organization等の業務テーブル更新
- schema変更
- role変更
- database作成

実際のGRANTはデプロイ先のIaCで管理し、アプリ起動用Secretとmigration用Secretを共有しません。

## 7. 監視network

監視用networkを事前作成します。

```bash
docker network create --internal vaultsend-monitoring
```

APIコンテナを同networkへ接続し、network内の名前を`vaultsend-api`、portを`8080`として到達可能にします。

```bash
docker network connect --alias vaultsend-api vaultsend-monitoring <api-container-name>
```

Prometheus、Alertmanager、Grafanaは同networkへ接続します。

Prometheus・Alertmanager・Grafanaのhost portは`127.0.0.1`へだけbindします。外部から参照する場合は、認証・TLS・アクセス制御を備えた管理用reverse proxyまたはVPN経由に限定します。

## 8. 起動

環境変数例をコピーします。

```bash
cd deploy/compose
cp .env.example .env
```

`.env`内のイメージ、Secretファイルpath、network名を環境に合わせて変更します。

設定展開だけを確認:

```bash
docker compose -f operations.yml config
```

起動:

```bash
docker compose -f operations.yml up -d
```

状態確認:

```bash
docker compose -f operations.yml ps
docker compose -f operations.yml logs --tail=100 audit-worker
docker compose -f operations.yml logs --tail=100 prometheus
docker compose -f operations.yml logs --tail=100 alertmanager
```

停止:

```bash
docker compose -f operations.yml down
```

データvolumeを削除する`down -v`は通常運用では実行しません。

## 9. Metrics

PrometheusはAPIの以下を30秒間隔で取得します。

```text
GET /internal/metrics
```

Bearer tokenは`authorization.credentials_file`から読み込みます。

主なMetrics:

- `up{job="vaultsend-api"}`
- `vaultsend_audit_outbox_scrape_success`
- `vaultsend_audit_outbox_pending`
- `vaultsend_audit_outbox_oldest_pending_age_seconds`
- `vaultsend_audit_outbox_oldest_pending_created_timestamp_seconds`

## 10. Alert

### Critical

- API内部Metricsへ2分間到達できない
- Outbox Metricが5分間存在しない
- DB集約クエリが2分間失敗
- 最古未処理イベントが5分を超えた状態が5分間継続

### Warning

- pendingが1000件を超えた状態が5分間継続
- 10分間でpendingが100件を超えて増加し、その状態が5分間継続

閾値は初期値です。実際の送信量、許容監査遅延、DB性能を計測したうえで変更します。

## 11. Alertmanager

通知先は`alertmanager-webhook-url`のSecretファイルから読み込みます。

- Critical: 1時間ごとに再通知
- Warning: 4時間ごとに再通知
- 復旧通知: 有効
- 1回のwebhook最大alert数: 50

通知先webhookは次を満たす必要があります。

- HTTPS
- 認証または推測困難なSecret URL
- 受信側でrate limit・重複排除を行う
- alert payloadを長期間無制限に保存しない
- 個人情報やアクセストークンをログへ出力しない

## 12. Grafana

Grafanaには次のダッシュボードを自動provisionします。

```text
VaultSend / Audit Outbox
```

表示内容:

- API Metrics到達性
- Outbox集約クエリ成功可否
- pending件数
- 最古未処理経過時間
- pending件数の時系列
- 最古未処理時間の時系列

UI上の編集は無効です。変更はJSONをPull Requestでレビューして反映します。

## 13. 障害対応

### `VaultSendAPIMetricsUnavailable`

1. APIプロセス・containerの死活を確認
2. PrometheusからAPIへのnetwork・DNSを確認
3. `INTERNAL_METRICS_TOKEN`とPrometheus Secretが一致するか確認
4. APIの401・404ログを確認

### `VaultSendAuditOutboxMetricsQueryFailed`

1. APIからPostgreSQLへ接続可能か確認
2. API用DB roleがOutbox集約SELECTを実行可能か確認
3. `INTERNAL_METRICS_QUERY_TIMEOUT_SEC`とDB負荷を確認
4. APIログの`event=internal_metrics_query_failed`を確認

### `VaultSendAuditOutboxDeliveryDelayed`

1. audit-worker containerが起動中か確認
2. workerログの`security_audit_outbox_run_failed`を確認
3. worker用DB Secretとrole権限を確認
4. PostgreSQL lock・接続数・IO負荷を確認
5. 復旧後にpendingと最古時間が減少することを確認

監査Outbox行を手動削除してアラートだけを解消してはいけません。最終監査テーブルへの配送確認なしに削除すると、監査証跡が欠落します。

## 14. バックアップ・保持

- Prometheus保持期間の初期値は15日
- audit-workerの処理済みOutbox保持期間の初期値は7日
- 最終監査イベントの保持方針はOutboxと分離する
- Grafana dashboardはGitで管理する
- Prometheus local volumeを監査証跡の原本として扱わない

監査証跡の原本は`security_audit_events`です。PrometheusとGrafanaは運用監視用途であり、監査保管の代替ではありません。

## 15. 設定検証

```bash
make verify-operations
```

検証内容:

- VaultSend container build
- runtime UID/GIDが`65532:65532`
- Docker Compose設定展開
- Prometheus設定
- Prometheus alert rule
- Alertmanager設定
- Grafana dashboard JSON

GitHub Actionsの`Operations` Workflowでも同じ検証を実行します。

## 16. リリース前確認

- [ ] アプリイメージをdigest固定した
- [ ] Prometheus・Alertmanager・Grafanaを検証済みdigestへ固定した
- [ ] worker専用DB roleを作成した
- [ ] API・worker・migrationのSecretを分離した
- [ ] SecretファイルをGit管理外にした
- [ ] 監視networkをinternalとして作成した
- [ ] 管理UIをインターネットへ直接公開していない
- [ ] Alertmanagerの通知テストを実施した
- [ ] Critical alertの担当者・連絡経路を決定した
- [ ] Grafana管理者パスワードを初期値から変更した
- [ ] backup・restore手順を確認した

## 17. 対象外・後続

- AWS ECS／EKS等のクラウド固有Terraform
- container imageの署名・SBOM・SLSA provenance
- vulnerability scanと更新自動化
- Alertmanagerの高可用化
- Prometheusの長期保存・remote write
- audit-worker専用heartbeat Metric
- DB role作成・GRANTのIaC
- 通知先サービス固有のtemplate

クラウド構成決定後、上記を別PRで追加します。
