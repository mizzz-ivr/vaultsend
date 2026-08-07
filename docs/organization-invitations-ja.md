# 組織招待フロー

## 目的

組織管理者がユーザーIDを共有せず、メールアドレスだけで既存・未登録ユーザーを安全に組織へ招待できるようにする。

## 対象範囲

- owner/adminによる招待作成・一覧・再送・取消
- admin/member権限の指定
- 期限付き招待リンクのメール送信
- ログインまたは新規登録後の招待承認
- 招待先とログインユーザーのメールアドレス一致確認
- seat上限の事前・トランザクション内検証
- Desktop/MobileのWeb導線

owner権限の招待、ドメイン自動参加、SCIM、SSO、Expoアプリは対象外とする。

## API

| Method | Path | 認証 | 必要権限 | 用途 |
|---|---|---:|---|---|
| POST | `/v1/orgs/{id}/invitations` | 必須 | owner/admin | 招待作成 |
| GET | `/v1/orgs/{id}/invitations` | 必須 | owner/admin | 招待一覧 |
| POST | `/v1/orgs/{id}/invitations/{invitation_id}/resend` | 必須 | owner/admin | 招待再送 |
| DELETE | `/v1/orgs/{id}/invitations/{invitation_id}` | 必須 | owner/admin | 招待取消 |
| GET | `/v1/invitations/{token}` | 不要 | なし | 招待内容の公開確認 |
| POST | `/v1/invitations/{token}/accept` | 必須 | 招待先本人 | 招待承認 |

公開確認APIはメールアドレス全文を返さず、先頭1文字以外をマスクする。

## データモデル

`organization_invitations`に以下を保存する。

- 組織ID
- 招待先メールアドレスと正規化済みメールアドレス
- 招待ロール（admin/member）
- 招待トークンのSHA-256ハッシュ
- 状態（pending/accepted/revoked）
- 招待者、承認者
- 有効期限、最終送信日時、承認日時、取消日時

期限切れはDB上の状態を増やさず、`pending`かつ`expires_at <= now`の場合にAPI表示上`expired`として扱う。再送または新規作成時には同一組織の期限切れpendingをrevokedへ更新する。

## セキュリティ

### トークン

- CSPRNGで32バイトを生成し、Base64URL形式にする
- 平文はメールキューへ渡す時だけ利用する
- DBにはSHA-256ハッシュのみ保存する
- 再送時は新しいトークンへローテーションする
- 旧トークンはハッシュが一致しなくなるため利用できない
- 取消済み、期限切れ、承認済みトークンは新規承認できない

### 本人確認

承認APIでは、セッションから取得したログインユーザーの正規化済みメールアドレスと、招待先の正規化済みメールアドレスを比較する。URLを転送された別ユーザーは承認できない。

### 権限

- 招待作成・一覧・再送・取消はowner/adminのみ
- 招待できる権限はadmin/memberのみ
- owner移譲は別機能として扱う
- UIで非表示にするだけでなく、APIのRBACを必須とする

## seat上限と同時実行

サービス層で現在メンバー数と有効なpending招待数を確認し、早期にエラーを返す。

同時リクエストによる上限超過を防ぐため、Store層でも次を行う。

1. 対象organization行を`FOR UPDATE`でロック
2. 最新のorganization subscriptionからseat上限を決定
3. 招待作成時は「現在メンバー + 有効なpending招待」を再集計
4. 招待承認時は現在メンバー数を再集計
5. 上限以内の場合のみ同一トランザクションで作成または承認する

無料または有効なPro契約がない組織のseat上限は1とする。Proの`active`/`trialing`は`seat_count`を使用する。

## メール送信

既存のSQS/SESメールワーカーを再利用する。

- APIは招待レコード作成後にSQSへ投入する
- ワーカーは`organization_invitation`テンプレートを選択する
- メールには組織名、権限、招待者、有効期限、`/invite/{token}`を記載する
- 初回キュー投入失敗時は招待をrevokedへ戻し、利用不能なpending招待を残さない
- 再送失敗時は再度再送できる。旧トークンは安全のため復元しない

## Web導線

### `/invitations`

- 所属組織の切替
- owner/admin向け招待作成
- 招待履歴、状態、有効期限、最終送信時刻
- pending/expiredの再送
- pendingの取消
- memberには権限不足メッセージを表示

### `/invite/[token]`

- 公開状態で組織名、マスク済みメール、権限、有効期限を表示
- 未ログイン時は`/auth?next=/invite/{token}`へ誘導
- ログイン・新規登録後は元の招待URLへ戻す
- 承認成功後は組織管理と送信履歴へ誘導

## エラー設計

| Code | HTTP | 意味 |
|---|---:|---|
| `invalid_email` | 400 | メール形式不正 |
| `invalid_role` | 400 | admin/member以外 |
| `invitation_not_found` | 404 | トークンまたは招待ID不明 |
| `invitation_exists` | 409 | 同一組織・メールの有効な招待が存在 |
| `member_exists` | 409 | 既に組織所属済み |
| `invitation_email_mismatch` | 403 | 招待先とログインメールが不一致 |
| `SEAT_LIMIT` | 403 | seat上限 |
| `invitation_expired` | 410 | 期限切れ |
| `invitation_revoked` | 410 | 取消済み |
| `invitation_mail_unavailable` | 503 | メールキュー利用不能 |

## テスト観点

### 正常系

- owner/adminによる作成・一覧・再送・取消
- 既存ユーザーと未登録ユーザーの招待
- ログイン後の承認
- 新規登録後の元URL復帰

### 異常系

- memberによる管理API操作
- メール不一致
- 不正・期限切れ・取消済み・旧トークン
- 重複招待、二重承認
- SQS/SES障害

### 境界・競合

- seat残数0
- 同時招待作成
- 同時招待承認
- 招待作成と承認の同時実行
- 期限時刻ちょうど

### 回帰

- 組織管理・課金
- 認証nextパラメータ
- ファイル送信・受信・送信履歴
