# 行政利用を見据えたセキュリティ基盤 第1段階

## 1. 目的

VaultSendを行政機関・地方公共団体を含む高いセキュリティ要求の利用環境へ近づけるため、アプリケーション境界の基本防御を強化します。

本対応は、ISMAP登録、政府情報システムの認証、第三者保証、行政利用可能性を単独で証明するものではありません。実際の採用には、対象業務・取り扱う情報区分・クラウド構成・運用体制を含むリスク分析、管理策の設計、証跡整備、第三者評価が必要です。

## 2. 参照する考え方

- デジタル庁「政府情報システムにおけるセキュリティ・バイ・デザインガイドライン（DS-200）」
- デジタル庁「政府情報システムにおけるセキュリティリスク分析ガイドライン（DS-201）」
- デジタル庁「政府情報システムにおける脆弱性診断導入ガイドライン（DS-221）」
- デジタル庁「政府情報システムにおける脅威の検知・対応のためのログ取得・分析導入ガイドブック（DS-222）」
- ISMAP管理基準のリスクベース・統制目標の考え方
- OWASP ASVS 5.0のWeb、API、認証、セッション、認可、暗号、ログに関する要求

## 3. 今回の対象範囲

### 3.1 CSRF防御

Cookie認証を利用する更新系リクエストに対して、以下を検証します。

- `Origin`が許可済みOriginと一致すること
- `Sec-Fetch-Site: cross-site`ではないこと
- 認証Cookieがある更新系リクエストで`Origin`が欠落していないこと

Cookieを持たないStripe webhook等のサーバー間リクエストは、`Origin`がない場合に継続利用できます。

許可Originは`FRONTEND_URL`のOriginを必ず含み、追加Originを`CSRF_ALLOWED_ORIGINS`で指定できます。

```bash
CSRF_ALLOWED_ORIGINS='https://admin.example.go.jp,https://app.example.go.jp'
```

### 3.2 信頼プロキシ境界

`X-Forwarded-For`は、直接接続元が`TRUSTED_PROXY_CIDRS`に含まれる場合のみ評価します。

```bash
TRUSTED_PROXY_CIDRS='10.0.0.0/8,2001:db8:1::/64'
```

注意事項:

- `0.0.0.0/0`や`::/0`を指定しないでください。
- ALB、リバースプロキシ、Ingress等の実際の接続元CIDRだけを登録してください。
- 複数プロキシの場合は右端から信頼済みプロキシを除外し、最初の未信頼アドレスをクライアントIPとして採用します。
- 未設定時は`X-Forwarded-For`を無視し、直接接続元を利用します。

同じ判定を以下へ適用します。

- レート制限
- アクセスログのIPハッシュ
- 認証セッションのIPハッシュ

### 3.3 ログの機微情報保護

- 生IPは保存せずSHA-256ハッシュを記録します。
- `/v1/access/{token}`等の受信トークンを含むURLはテンプレート化して記録します。
- request ID、HTTP method、正規化済みendpoint、status、response bytes、処理時間を記録します。
- レート制限ログも生IP・受信トークンを出力しません。

今回のログは標準出力です。行政利用を想定する本番環境では、改ざん耐性、アクセス制御、保管期間、時刻同期、SIEM連携を別途設計する必要があります。

### 3.4 レート制限のリソース枯渇対策

- エンドポイント中のtoken・IDを正規化し、カウンターの不要な高カーディナリティ化を抑制します。
- インメモリカウンターは最大10万件とし、上限到達時は新規キーをfail-closedで拒否します。
- 期限切れカウンターを上限確認時に削除します。

制約:

- 現在は単一プロセス内の制限です。
- 複数インスタンス運用ではWAF、API Gateway、Redis等の共有レート制限へ移行してください。

### 3.5 APIのHTTPセキュリティヘッダー

以下を付与します。

- `Cache-Control: no-store`
- `Content-Security-Policy`
- `Cross-Origin-Opener-Policy`
- `Cross-Origin-Resource-Policy`
- `Permissions-Policy`
- `Referrer-Policy`
- `Strict-Transport-Security`（local/test以外）
- `X-Content-Type-Options`
- `X-Frame-Options`
- `X-Permitted-Cross-Domain-Policies`

### 3.6 WebのCSP・HTTPセキュリティヘッダー

Next.jsの全レスポンスへCSPと防御ヘッダーを付与し、`X-Powered-By`を無効化します。

本番CSPの`connect-src`は、標準で以下を許可します。

- 同一Origin
- `https://*.amazonaws.com`（S3 Presigned URLへの直接アップロード）

追加のオブジェクトストレージ等を利用する場合のみ、必要なOriginを明示します。

```bash
VAULTSEND_CSP_CONNECT_SRC='https://storage.example.go.jp'
```

ワイルドカードや`https:`全体の許可は避けてください。

### 3.7 本番設定のfail-closed

`APP_ENV`が`local`または`test`以外の場合、以下を満たさない設定では起動しません。

- `COOKIE_SECURE=true`
- `HSTS_ENABLED=true`
- `FRONTEND_URL`がHTTPS
- `COOKIE_SAMESITE=none`の場合はSecure Cookie
- `TRUSTED_PROXY_CIDRS`が指定されている場合は全値が正しいCIDR
- CSRF許可Originが正しいHTTP(S) Origin

### 3.8 HTTPサーバーのリソース上限

既定値:

```text
HTTP_REQUEST_TIMEOUT_SEC=30
HTTP_READ_HEADER_TIMEOUT_SEC=5
HTTP_READ_TIMEOUT_SEC=15
HTTP_WRITE_TIMEOUT_SEC=35
HTTP_IDLE_TIMEOUT_SEC=60
HTTP_MAX_HEADER_BYTES=32768
```

`HTTP_WRITE_TIMEOUT_SEC`はmiddlewareの`HTTP_REQUEST_TIMEOUT_SEC`より長くしてください。

## 4. 運用確認

### 4.1 本番起動前

- TLS終端からアプリまでの通信経路を確認する
- `TRUSTED_PROXY_CIDRS`を実ネットワーク構成と照合する
- `FRONTEND_URL`と`CSRF_ALLOWED_ORIGINS`を棚卸しする
- HSTS対象ドメイン・サブドメインがすべてHTTPS対応済みであることを確認する
- CSPに不要なOriginがないことを確認する
- ログ転送先のアクセス制御・保管期間・削除手順を確認する

### 4.2 テスト

```bash
go test ./internal/config ./internal/http/middleware ./internal/http/handler
go test ./...
cd web
npm run lint
npm run typecheck
npm run build
npm run e2e
```

## 5. 次段階の優先項目

1. 行政・組織利用向けSSO（OIDC/SAML）と必須MFA
2. セキュリティ監査ログの専用テーブル・追記専用保存・外部SIEM転送
3. ファイルのマルウェアスキャン、隔離、検疫状態、スキャン完了前ダウンロード禁止
4. KMSを利用した顧客・組織単位の暗号鍵管理と鍵ローテーション
5. WAF・共有レート制限・Bot対策・DDoS防御
6. 組織境界のDB強制（PostgreSQL RLS等）と越境テスト
7. SBOM、依存脆弱性スキャン、CodeQL、Secret scanning、コンテナスキャン
8. バックアップ暗号化、復元訓練、RTO/RPO、マルチAZ・DR設計
9. 脆弱性診断・侵入テストと是正証跡
10. 情報分類、保存期間、リーガルホールド、完全削除の運用設計
11. インシデント対応手順、連絡網、証拠保全、演習
12. ISMAP等の管理策と実装・運用証跡のトレーサビリティ表

## 6. 完了条件

- 信頼されていない接続元からの`X-Forwarded-For`偽装でレート制限を回避できない
- Cookie認証された更新系APIを許可外Originから実行できない
- アクセストークンと生IPが通常アクセスログ・レート制限ログへ出力されない
- local/test以外でHTTPS・Secure Cookie・HSTSを無効化すると設定エラーになる
- APIとWebに定義したセキュリティヘッダーが付与される
- HTTPサーバーの時間・ヘッダー上限が設定される
- Go・Web・PostgreSQL・Playwrightの既存CIが成功する
