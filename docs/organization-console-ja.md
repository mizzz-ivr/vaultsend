# 組織管理・課金コンソール

## 目的

バックエンドに実装済みの Organization / RBAC / Stripe 課金・請求書 API を、Webから利用できるようにします。法人利用者が組織の切替、メンバー管理、seat確認、請求書確認を一つの画面で完結できることを目的とします。

## 画面

- URL: `/organizations`
- 認証: 必須
- 未ログイン時: `/auth` へ遷移

## 主な機能

### 組織

- 所属組織一覧の取得
- 選択中組織の切替
- 組織が0件の場合を含む新規組織作成
- 組織ID・作成者ユーザーIDの表示

### メンバー

- 組織メンバーと権限の表示
- `owner` / `admin` によるメンバー追加
- `owner` / `admin` によるメンバー削除
- 自分自身と `owner` ロールの削除操作はUI上で非表示

> 現在のAPIはユーザーID（UUID）指定でメンバーを追加します。メール招待は本タスクの対象外です。

### 課金

`owner` のみ以下を表示します。

- 現在のプラン・契約状態
- 直近30日のshipment数
- 現在の保存容量
- seat利用数・上限・残数
- seat使用率
- 次回請求日
- Stripe Checkout開始

### 請求書

`owner` / `admin` に以下を表示します。

- 金額
- 発行日
- ステータス
- Stripe Hosted Invoice URL
- PDF URL
- cursor方式による追加読み込み

## 権限マトリクス

| 機能 | owner | admin | member |
| --- | --- | --- | --- |
| 組織詳細 | 可 | 可 | 可 |
| メンバー一覧 | 可 | 可 | 可 |
| メンバー追加・削除 | 可 | 可 | 不可 |
| 課金・seatサマリー | 可 | 不可 | 不可 |
| 請求書一覧 | 可 | 可 | 不可 |
| Checkout開始 | 可 | 不可 | 不可 |

## API

- `GET /v1/orgs`
- `POST /v1/orgs`
- `GET /v1/orgs/{id}`
- `POST /v1/orgs/{id}/members`
- `DELETE /v1/orgs/{id}/members/{user_id}`
- `GET /v1/orgs/{id}/billing`
- `GET /v1/orgs/{id}/invoices`
- `POST /v1/billing/checkout`

## エラー設計

組織詳細の取得失敗は画面全体のエラーとして扱います。課金情報と請求書は外部サービス依存のため、個別に失敗を表示し、メンバー管理など他の操作を継続できるようにしています。

- `401`: 認証画面へ遷移
- `403`: APIメッセージを表示
- Stripe未設定・一時障害: 課金または請求書セクションだけに表示
- 通信障害: 操作対象に応じた日本語メッセージを表示

## セキュリティ

- Cookie認証を継続し、API Clientは `credentials: include` を使用
- 権限判定のsource of truthはバックエンドとし、UI制御だけに依存しない
- Checkout URLはHTTPS（ローカルのみlocalhostを許可）に限定
- 請求書リンクは新規タブで開き、`rel="noreferrer"` を設定
- 組織・メンバーIDは表示するが、アクセストークンやStripe secretは扱わない

## テスト観点

### 正常系

- owner組織の表示
- admin組織への切替
- 組織0件からの作成
- メンバー追加
- 課金サマリー表示
- 請求書表示

### 異常系

- 未ログイン
- 組織詳細取得失敗
- 課金情報のみ取得失敗
- 請求書のみ取得失敗
- seat上限超過
- Stripe Checkout失敗

### 境界値

- 組織0件
- メンバー1件
- seat使用率0% / 100%
- 請求書0件
- 請求書の次ページあり / なし

### 回帰

- 送信ウィザード
- 送信履歴
- 受信者ダウンロード
- セキュリティヘッダー

## Expoへの拡張方針

今回の差分は既存Next.jsへ限定します。Expoアプリを追加する場合も、同じOrganization / Billing APIと権限マトリクスを利用できます。ネイティブ側ではCookieセッションの安全な保持、外部Checkoutのブラウザ遷移、請求書URLの外部リンク処理を別途設計します。WebとExpoのUIコード共有を先に目的化せず、API型と業務ルールの共有を優先します。
