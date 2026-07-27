# 内部監視Metrics設計・運用

## 1. 目的

監査Outboxの配送停止や滞留を外部監視から検知できるようにします。

既存の`GET /healthz`はAPIプロセスの最小livenessとして維持します。監査Outboxが滞留している場合にAPIプロセスを自動再起動しても配送workerの復旧にはならないため、livenessと業務監視を分離します。

## 2. エンドポイント

```text
GET /internal/metrics
```

Prometheus text exposition formatを返します。

`INTERNAL_METRICS_TOKEN`が未設定の場合はHTTP 404を返し、エンドポイントを無効化します。

設定済みの場合は以下のBearer認証が必要です。

```http
Authorization: Bearer <INTERNAL_METRICS_TOKEN>
```

認証失敗時はHTTP 401を返し、DBクエリは実行しません。

## 3. 設定

```bash
# 32文字以上。Secret Manager等から注入する。
export INTERNAL_METRICS_TOKEN='replace-with-at-least-32-random-characters'

# DB集約クエリのタイムアウト。既定3秒。
export INTERNAL_METRICS_QUERY_TIMEOUT_SEC=3
```

トークンには空白文字を含められません。

本番ではトークンをソースコード、Docker image、ログ、監視設定リポジトリへ直接記載しません。Secret ManagerからAPIと監視基盤へ別経路で注入します。

## 4. Metrics

### `vaultsend_audit_outbox_scrape_success`

監査Outbox集約クエリの成功可否です。

- `1`: 成功
- `0`: DBエラー、タイムアウト、Store未設定

クエリ失敗時はHTTP 503を返し、pending件数を0として出力しません。監視不能状態を「滞留なし」と誤認しないためです。

### `vaultsend_audit_outbox_pending`

`processed_at IS NULL`の監査Outbox件数です。

通常は短時間で0へ戻ります。継続的に増加する場合はaudit-worker停止、DB権限不足、配送SQL失敗を疑います。

### `vaultsend_audit_outbox_oldest_pending_age_seconds`

最古の未処理Outboxが作成されてからの経過秒です。

未処理Outboxがない場合は0です。

### `vaultsend_audit_outbox_oldest_pending_created_timestamp_seconds`

最古の未処理OutboxのUnix timestampです。

未処理Outboxがない場合は0です。監視画面で発生時刻を表示する用途に使用できます。

## 5. レスポンス例

```text
# HELP vaultsend_audit_outbox_scrape_success Whether the audit outbox metrics query succeeded.
# TYPE vaultsend_audit_outbox_scrape_success gauge
vaultsend_audit_outbox_scrape_success 1
# HELP vaultsend_audit_outbox_pending Number of unprocessed security audit outbox events.
# TYPE vaultsend_audit_outbox_pending gauge
vaultsend_audit_outbox_pending 3
# HELP vaultsend_audit_outbox_oldest_pending_age_seconds Age in seconds of the oldest unprocessed security audit outbox event.
# TYPE vaultsend_audit_outbox_oldest_pending_age_seconds gauge
vaultsend_audit_outbox_oldest_pending_age_seconds 42.5
# HELP vaultsend_audit_outbox_oldest_pending_created_timestamp_seconds Unix timestamp of the oldest unprocessed security audit outbox event. Zero when no event is pending.
# TYPE vaultsend_audit_outbox_oldest_pending_created_timestamp_seconds gauge
vaultsend_audit_outbox_oldest_pending_created_timestamp_seconds 1.7851234e+09
```

## 6. ローカル確認

```bash
curl -sS http://localhost:8080/internal/metrics \
  -H "Authorization: Bearer ${INTERNAL_METRICS_TOKEN}"
```

未設定時の確認:

```bash
unset INTERNAL_METRICS_TOKEN
curl -i http://localhost:8080/internal/metrics
# HTTP 404
```

認証失敗時の確認:

```bash
curl -i http://localhost:8080/internal/metrics \
  -H 'Authorization: Bearer invalid-token'
# HTTP 401
```

## 7. Prometheus設定例

Bearer tokenをコマンドライン引数や平文設定へ直接書かず、権限を制限したsecret fileを利用します。

```yaml
scrape_configs:
  - job_name: vaultsend-api
    metrics_path: /internal/metrics
    scheme: https
    bearer_token_file: /run/secrets/vaultsend-internal-metrics-token
    static_configs:
      - targets:
          - vaultsend-api.internal.example.go.jp
```

インターネット公開経路から直接到達させず、private subnet、internal load balancer、service mesh等の内部経路に限定します。Bearer tokenはネットワーク境界の代替ではなく追加防御です。

## 8. 推奨アラート

### Metrics取得失敗

```promql
vaultsend_audit_outbox_scrape_success == 0
```

1分以上継続した場合に警告します。

### Metrics自体が消失

```promql
absent(vaultsend_audit_outbox_scrape_success)
```

API停止、監視経路断、認証設定不整合を検知します。

### 最古未処理が5分超過

```promql
vaultsend_audit_outbox_oldest_pending_age_seconds > 300
```

監査イベントの配送遅延として高優先度で通知します。

### Pending件数が1000件超過

```promql
vaultsend_audit_outbox_pending > 1000
```

急激な流量増加またはworker停止を疑います。

実運用では件数だけでなく、増加率と最古経過秒を組み合わせて判断します。

## 9. 障害時の確認順序

1. `vaultsend_audit_outbox_scrape_success`が1か確認する
2. APIとPostgreSQL間の接続状態を確認する
3. audit-workerプロセスの死活を確認する
4. `event=security_audit_outbox_run_failed`ログを確認する
5. audit-worker用DB roleの権限を確認する
6. pending件数と最古作成時刻を確認する
7. 原因解消後、pendingが減少し0へ戻ることを確認する

Outbox行を手動削除してアラートだけを解消してはいけません。最終監査テーブルへの配送確認なしに削除すると監査証跡が欠落します。

## 10. セキュリティ

- トークン比較は定数時間比較を使用します
- 認証前にDBへアクセスしません
- レスポンスには件数と時刻だけを含め、shipment ID、organization ID、actor情報を含めません
- `Cache-Control: no-store`を設定します
- DBエラー詳細はレスポンスへ返さず、request ID付きのサーバーログだけへ記録します
- 監査middlewareのイベント分類対象外とし、定期スクレイプで監査イベントを大量生成しません

## 11. テスト観点

### Unit test

- token未設定時は404
- token欠落・不一致・不正形式は401
- 認証失敗時はStoreを呼ばない
- 正常時はPrometheus形式を返す
- DB失敗時は503と`scrape_success 0`
- Query timeout時は503
- configで32文字未満・空白入りtokenを拒否

### PostgreSQL integration test

- 未処理2件でpending=2、最古行の時刻と経過秒を返す
- 最古行を処理するとpending=1、次の行が最古になる
- 全件処理後はpending・経過秒・timestampが0になる

## 12. 対象外

- audit-worker自体のHTTP metrics server
- 配送成功総数・失敗総数のプロセス内counter
- OpenTelemetry Collector連携
- Grafana dashboard・AlertmanagerのIaC
- API・worker・migration用DB roleの実作成

これらはデプロイ・IaCと合わせて別PRで対応します。
