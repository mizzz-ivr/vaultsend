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
- 署名・provenance・SBOM検証済みdigestだけを起動するデプロイ前ゲート

## 2. 適用範囲

`deploy/compose/operations.yml`は、単一ホストまたは検証環境での運用リファレンスです。

本番でAWS ECS、Kubernetes等を採用する場合も、次の責務と設定を移植します。

- APIとaudit-workerを別プロセス／別サービスとして起動する
- worker用DB資格情報をAPI用資格情報と分離する
- `/internal/metrics`は内部ネットワークからのみ到達可能にする
- Prometheus ruleとGrafana dashboardを同一内容で配備する
- Secretを平文環境変数、Git、コンテナイメージへ格納しない
- 期待するrepository・workflow・refから署名されたdigestだけを実行する

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

## 8. 検証済みイメージの起動

### 8.1 前提コマンドと認証

デプロイhostへ以下を導入します。

- Docker Engine・Docker Compose
- Cosign
- GitHub CLI
- jq

GHCRとGitHub CLIを事前認証します。

```bash
docker login ghcr.io
gh auth login
```

非対話実行では、最小権限のcredentialをDocker credential storeと`GH_TOKEN`から参照します。credentialを`.env`、shell履歴、Gitへ保存しません。

### 8.2 環境変数・Secret準備

```bash
cd deploy/compose
cp .env.example .env
cd ../..
```

`.env`内のSecretファイルpath、network名、監視設定を環境に合わせて変更します。

`.env`の`VAULTSEND_IMAGE`はplaceholderです。実際のデプロイではwrapperが検証済みdigestで上書きします。`.env`を使った直接の`docker compose up`は禁止します。

### 8.3 デプロイ前チェック

GitHub ActionsのSupply Chain Workflowが公開した、完全なdigest参照を指定します。

```bash
export VAULTSEND_IMAGE='ghcr.io/mizzz-ivr/vaultsend@sha256:<64桁のdigest>'
make check-operations-deploy
```

`--check`相当では以下を行います。

1. repositoryが`ghcr.io/mizzz-ivr/vaultsend`と完全一致するか確認
2. SHA-256 digest固定か確認
3. digest指定でイメージをpull
4. pull後のRepoDigest一致確認
5. OCI source labelが公式repositoryと一致するか確認
6. OCI revision labelが40桁のcommit SHAか確認
7. Cosign署名を期待するGitHub Actions identityとOIDC issuerで検証
8. GitHub build provenanceをrepository・signer workflow・`refs/heads/main`・source digest固定で検証
9. SPDX SBOM Attestationを同じidentity条件で検証
10. Compose設定を展開・検証

イメージのlocal cacheは更新しますが、コンテナの作成・再起動・停止は行いません。

### 8.4 デプロイ

事前チェックと同じdigestを指定し、明示的なデプロイコマンドを実行します。

```bash
export VAULTSEND_IMAGE='ghcr.io/mizzz-ivr/vaultsend@sha256:<64桁のdigest>'
make deploy-operations
```

wrapperは署名・provenance・SPDX SBOMを再検証し、すべて成功した場合だけ以下を実行します。

```bash
docker compose --env-file deploy/compose/.env \
  -f deploy/compose/operations.yml \
  up -d --remove-orphans --no-build
```

`VAULTSEND_IMAGE`は検証済みdigestでprocess環境から上書きされます。タグ指定、別repository、署名不一致、Attestation不足、source revision不一致の場合はCompose起動へ進みません。

### 8.5 検証証跡

既定では以下へ保存します。

```text
artifacts/deploy-verification/
├── requested-image.txt
├── image-inspect.json
├── source-revision.txt
├── cosign-verification.json
├── provenance-verification.json
├── sbom-verification.json
└── verification-summary.json
```

ディレクトリは権限`700`、生成ファイルは`umask 077`で作成します。symbolic linkの結果ディレクトリは拒否します。

本番監査では、デプロイ日時、実行者、対象環境、`verification-summary.json`、Compose状態、変更承認記録を関連付けて保管します。

### 8.6 状態確認・停止

```bash
cd deploy/compose
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

### デプロイ前ゲート失敗

1. タグではなく`ghcr.io/mizzz-ivr/vaultsend@sha256:<digest>`を指定しているか確認
2. `docker login ghcr.io`と`gh auth status`を確認
3. OCI source・revision labelを確認
4. Cosign certificate identityが`Supply Chain Security` Workflowの`main`実行か確認
5. provenanceとSPDX SBOM Attestationが同じdigest・source commitに存在するか確認
6. 検証に失敗したdigestを起動しない
7. 正規Workflowから再build・再署名し、新しいdigestを発行する

検証失敗したイメージへ手動で署名だけを後付けし、正規リリース扱いにはしません。

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
