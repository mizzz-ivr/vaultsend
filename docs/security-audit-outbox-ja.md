# セキュリティ監査ログ outbox 設計・運用

## 1. 目的

重要な業務更新と監査イベントの準備記録を同一PostgreSQL transactionでcommitし、API処理成功後に監査ログだけ欠落する状態を防止します。

本機能は`security_audit_events`を置き換えるものではありません。`security_audit_outbox`は配送用の一時テーブルであり、最終的な監査証跡・検索・完全性確認は引き続き追記専用の`security_audit_events`を使用します。

## 2. 今回の対象

以下の成功系DB更新をoutbox対応しました。

- `shipment.create`
  - shipment確定
  - file紐付け
  - recipient/access token作成
  - 監査outbox INSERT
- `shipment.delete`
  - shipment論理削除
  - access token revoke
  - 監査outbox INSERT
- `upload.complete`
  - file作成
  - upload session完了
  - 監査outbox INSERT

認証失敗、認可拒否、入力エラー、outbox未対応操作は従来どおりHTTP middlewareから`security_audit_events`へ直接記録します。

## 3. 対象外

以下は本PRではoutbox化しません。

- organization member追加・削除
  - Stripe seat同期と補償処理があり、DB更新だけを成功扱いにできないため
- billing checkout
  - Stripe API呼び出しが主処理で、同一DB transactionが存在しないため
- billing webhook
  - subscription更新単位のtransaction再設計が必要なため
- access verify / download URL発行
  - token使用回数、download event、shipment countの一括transaction化と合わせて対応するため
- mail-worker / cleanup-worker
  - actor/source service設計とworker専用outboxを別PRで追加するため

## 4. 処理フロー

1. HTTP middlewareがrequest ID、ルートテンプレート、接続元、User-Agent、認証主体を取得する
2. HMAC署名済みイベントを作る関数をrequest contextへ格納する
3. 監査対応Storeが業務更新transaction内で操作固有のorganization/resource IDを補完する
4. 業務更新と`security_audit_outbox` INSERTを同一transactionでcommitする
5. API middlewareはoutbox登録済みの成功イベントを直接INSERTしない
6. `audit-worker`が未処理outboxを排他取得する
7. `security_audit_events` INSERTとoutboxの`processed_at`更新を同一transactionで実行する
8. 保持期間を超えた処理済みoutboxを削除する

## 5. 配送の冪等性

- outbox IDと最終監査イベントIDは同一UUIDです
- `security_audit_events.id`の主キーと`ON CONFLICT (id) DO NOTHING`を使用します
- 配送完了更新は監査イベントINSERTを含む同一SQL transaction内で実行します
- worker再起動や複数起動時も同じイベントを重複記録しません
- `FOR UPDATE SKIP LOCKED`により複数workerが同一outboxを同時処理しません

## 6. 障害時の挙動

### API transaction内でoutbox INSERTが失敗した場合

業務更新もrollbackし、APIは失敗を返します。

例:

- shipmentだけ削除され監査outboxがない状態にはしない
- fileだけ作成されupload完了監査がない状態にはしない

### audit-workerが停止した場合

- APIの業務更新は継続できます
- outboxは未処理のまま蓄積します
- worker復旧後に古い順で配送します
- pending件数の増加を監視対象とします

### 最終監査テーブルへの配送が失敗した場合

- transactionがrollbackされます
- outboxの`processed_at`は更新されません
- 次のpollで再試行します

## 7. 設定

```bash
DATABASE_URL='postgres://...'
AUDIT_OUTBOX_POLL_INTERVAL_SEC=2
AUDIT_OUTBOX_BATCH_SIZE=100
AUDIT_OUTBOX_RETENTION_HOURS=168
AUDIT_OUTBOX_CLEANUP_BATCH_SIZE=500
```

`audit-worker`は監査HMAC秘密鍵を読み込みません。HMACはAPI内でoutbox登録前に生成済みであり、workerの責務は同一DB内の冪等配送だけです。

## 8. 起動

```bash
make run-audit-worker
```

本番ではAPIとは別プロセス・別コンテナとして常時1台以上起動します。複数起動は可能ですが、最初は運用の単純さを優先して1台を推奨します。

## 9. 監視

最低限、以下を監視します。

- `security_audit_outbox`の未処理件数
- 最古の未処理`created_at`からの経過時間
- `event=security_audit_outbox_run_failed`
- `event=security_audit_outbox_processed`の停止
- audit-workerプロセスの死活

推奨アラート例:

- pending > 1000
- 最古未処理が5分以上
- worker errorが5分間に3回以上
- APIは稼働中だがworkerログが10分以上ない

## 10. 保持期間

処理済みoutboxは既定7日で削除します。

削除対象は配送用一時データのみです。追記専用`security_audit_events`のretentionには影響しません。

## 11. テスト観点

### 正常系

- 3対象操作で業務更新とoutboxが同時commitされる
- worker配送前は最終監査テーブルに存在しない
- worker配送後に最終監査テーブルへ1件だけ存在する
- 再配送しても重複しない
- 処理済みoutboxが保持期間後に削除される

### 異常系

- outbox制約違反時に業務更新もrollbackする
- worker配送失敗時に`processed_at`が更新されない
- contextへoutbox factoryがないservice単体テストでは既存Store動作を維持する

### 既存影響

- outbox未対応APIの監査記録
- 認証・認可失敗の直接監査
- shipment送信・受信E2E
- migration up/down/up
- API、mail-worker、cleanup-workerのbuild

## 12. 後続タスク

1. organization member変更とStripe seat同期の整合性設計
2. access verify/download成功処理のtransaction統合
3. billing webhook用outbox
4. mail-worker/cleanup-workerの監査イベント化
5. pending件数・最古時刻のmetrics公開
6. audit-workerのデプロイ/IaC追加
7. dead-letter状態と管理者向け再配送操作
8. 外部SIEM・WORMストレージへの配送
