# セキュリティ監査ログ設計・運用手順

## 1. 目的

VaultSendで発生した認証、組織管理、送信、受信、課金等の重要操作について、事後調査と不正操作検知に利用できる追記専用の監査証跡を残します。

本機能は行政機関・地方公共団体等の高いセキュリティ要求を意識した基盤ですが、この実装だけで行政利用可否、ISMAP適合、法令・組織基準への準拠を証明するものではありません。

## 2. 今回の対象範囲

- PostgreSQLの追記専用`security_audit_events`テーブル
- `UPDATE`、`DELETE`、`TRUNCATE`のDB triggerによる拒否
- 接続元IPとUser-AgentのHMAC-SHA256仮名化
- イベント主要項目のHMAC-SHA256完全性検証
- 認証・組織管理・shipment・download URL発行・課金操作の監査
- 組織owner/admin向け監査ログ照会API
- SIEM転送対象となる構造化アプリケーションログ

## 3. 対象外

- WORMストレージ、Object Lock等を用いた外部保全
- DB管理者権限を持つ攻撃者に対する完全な改ざん防止
- business transactionと監査イベント挿入の原子的なcommit
- HMAC鍵rotation時の旧鍵自動保持
- SIEM製品固有の転送設定
- 監査ログの自動削除・retention worker
- 監査ログのCSV/NDJSON export API

## 4. 保存項目

主な保存項目は以下です。

| 項目 | 内容 |
|---|---|
| `id` | 監査イベントUUID |
| `occurred_at` | 操作発生時刻 |
| `recorded_at` | DB記録時刻 |
| `event_type` | 操作種別 |
| `severity` | `info` / `warning` / `critical` |
| `outcome` | `success` / `denied` / `failure` |
| `actor_type` | `user` / `anonymous` / `recipient` / `system` / `webhook` |
| `actor_user_id` | 認証済みユーザーID。メールアドレスは保存しない |
| `organization_id` | 組織スコープが判明している場合の組織ID |
| `resource_type` | 操作対象種別 |
| `resource_id` | 操作対象UUID。token生値は保存しない |
| `request_id` | API request ID |
| `source_service` | `api` / `mail-worker` / `cleanup-worker` |
| `http_method` | HTTP method |
| `route_pattern` | ID・tokenを含まないchi route template |
| `status_code` | HTTP status |
| `client_ip_hmac` | IPのHMAC-SHA256仮名化値 |
| `user_agent_hmac` | User-AgentのHMAC-SHA256仮名化値 |
| `details` | allowlistされた補助情報のみ |
| `integrity_key_id` | HMAC鍵識別子 |
| `integrity_hmac` | 主要項目の完全性HMAC |

## 5. 保存禁止情報

以下は監査ログへ保存しません。

- password
- session token
- access token / manage token生値
- Stripe webhook secret
- Presigned URL
- ファイル名、メール本文、shipment本文
- 受信者メールアドレス
- HTTP request body
- URL query string
- 生IP
- 生User-Agent

`client_ip_hmac`と`user_agent_hmac`は匿名情報ではなく仮名化情報です。アクセス権、保管期間、外部転送範囲を制限してください。

## 6. 監査イベント一覧

| event_type | 対象操作 |
|---|---|
| `auth.register` | ユーザー登録 |
| `auth.login` | ログイン |
| `auth.logout` | ログアウト |
| `upload.create` | upload開始 |
| `upload.complete` | multipart upload完了 |
| `shipment.create` | shipment確定 |
| `shipment.resend` | 通知再送 |
| `shipment.delete` | shipment論理削除 |
| `access.verify` | 受信token/password検証 |
| `file.download_url.issue` | download URL発行 |
| `organization.create` | 組織作成 |
| `organization.member.add` | メンバー追加 |
| `organization.member.remove` | メンバー削除 |
| `security_audit.read` | 組織監査ログ閲覧 |
| `billing.checkout` | Stripe Checkout開始 |
| `billing.webhook` | Stripe webhook受信 |

## 7. HMAC設定

### 必須環境変数

```bash
AUDIT_LOG_HMAC_SECRET='32バイト以上のランダムな秘密値'
AUDIT_LOG_HMAC_KEY_ID='prod-2026-01'
```

`local` / `test`では未設定時に開発専用値を使用します。その他の環境では未設定、32バイト未満、不正なkey IDを起動時に拒否します。

### 秘密鍵管理

- AWS Secrets Manager等から実行時注入する
- Git、Docker image、Terraform state平文、CI logへ保存しない
- `ACCESS_GRANT_SECRET`等の別用途secretを流用しない
- API processと監査検証を行う管理processだけへ付与する

### rotation

現段階では1つの現行鍵だけを読み込みます。rotation前に旧鍵で記録されたイベントを外部保全し、旧鍵を監査検証専用環境へ保持してください。

旧鍵を破棄すると、旧イベントの`integrity_valid`は検証できません。複数検証鍵対応は後続課題です。

## 8. 追記専用制約

DB triggerにより以下を拒否します。

- `UPDATE security_audit_events ...`
- `DELETE FROM security_audit_events ...`
- `TRUNCATE security_audit_events`

SQLSTATEは`55000`です。

これは通常のアプリケーションDB権限による誤更新・不正更新を防ぐものです。DB owner、superuser、trigger無効化権限を持つ主体への完全な防御ではありません。

本番では以下を追加してください。

- アプリケーションDB roleからDDL権限を除外
- migration roleとruntime roleを分離
- CloudTrail、RDS audit log等でDDL・権限変更を監視
- 定期的に外部SIEMまたはObject Lock付きストレージへ転送

## 9. API

### 組織監査ログ一覧

```text
GET /v1/orgs/{organization_id}/security-audit-events?limit=50&offset=0
```

要件:

- ログイン必須
- 対象組織の`owner`または`admin`のみ
- `limit`既定値50、最大100
- `offset`は0以上
- IP/User-AgentのHMAC値と`integrity_hmac`自体はレスポンスへ返さない
- 各イベントに`integrity_valid`を返す

監査ログ閲覧操作自体も`security_audit.read`として記録します。

## 10. SIEM連携

監査イベントのDB保存成功時に、以下の形式で構造化ログを出力します。

```text
event=security_audit_persisted audit_event_id=... audit_event_type=... outcome=... severity=... request_id=...
```

保存失敗時:

```text
event=security_audit_persist_failed audit_event_type=... request_id=... error=...
```

本番では`security_audit_persist_failed`を即時アラート対象にしてください。

推奨アラート例:

- `severity=critical`
- `outcome=denied`の急増
- `auth.login`失敗の急増
- `organization.member.add/remove`
- `shipment.delete`
- `security_audit.read`
- `billing.webhook`の5xx
- `security_audit_persist_failed`が1件以上

## 11. 失敗時の挙動

監査middlewareは業務handler完了後に監査イベントを保存します。保存失敗時は構造化エラーログを出しますが、すでに完了した業務レスポンスは変更しません。

このため、現段階では業務更新と監査記録が完全に原子的ではありません。行政利用を本格化する前に、以下のいずれかへ移行してください。

- 業務transaction内でaudit outboxを同時insert
- security audit outboxをworkerが監査テーブル・外部SIEMへ配送
- DB logical decoding等でcommit済み変更を監査基盤へ連携

## 12. Retention

追記専用制約のため、通常の`DELETE`によるretentionは行いません。

将来は月次partitionを導入し、承認済み運用手順と専用migration roleでpartition単位の外部保全・削除を行う設計を推奨します。

## 13. テスト観点

### 正常系

- 対象API操作で1イベント記録される
- 認証成功後のactor user IDが記録される
- 組織作成・メンバー操作でorganization/resource IDが記録される
- owner/adminが組織監査ログを取得できる
- integrity HMACが正常なイベントで`integrity_valid=true`

### 異常系

- member権限の監査ログ閲覧を403で拒否
- 401/403操作を`outcome=denied`として記録
- 5xxを`severity=critical`として記録
- HMAC secret不備で本番起動を拒否
- 監査保存失敗時に`security_audit_persist_failed`を出力

### 改ざん・境界値

- UPDATE、DELETE、TRUNCATEをSQLSTATE 55000で拒否
- event type・details size・status code制約
- token付きURLがroute templateへ変換される
- 生IP・生User-AgentがDBへ保存されない
- event内容変更後にintegrity検証が失敗する
- limit 101を400で拒否

## 14. 後続タスク

1. business transactionと同一commitのaudit outbox
2. mail-worker / cleanup-workerの監査イベント接続
3. 複数HMAC検証鍵とrotation手順
4. 月次partitionとretention承認フロー
5. NDJSON exportとSIEM配送worker
6. CloudWatch Logs / Firehose / OpenSearch等の環境別構成
7. 監査ログ閲覧UIとエクスポート権限の分離
8. WORMストレージへの定期保全と復元訓練
