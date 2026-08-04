# 本番ロールバック承認・release台帳・実行手順

## 1. 目的

本番障害時に、VaultSendを任意の過去イメージではなく、次の条件をすべて満たすreleaseだけへ戻します。

- 公式GHCR repositoryのdigest固定イメージ
- Supply Chain WorkflowによるCosign署名付き
- GitHub build provenance付き
- SPDX SBOM Attestation付き
- `main`のfirst-parent履歴に存在するrevision
- 現在revisionの祖先
- この本番hostで過去に正常デプロイされた履歴がある
- `production-rollback` Environmentで別担当者が承認した
- 30分以内のAttestation付き許可証がある

自動ロールバックは行いません。起動者、承認者、本番hostの実行者を分離します。

## 2. 対象範囲

本機能が戻す対象は`deploy/compose/operations.yml`で管理するコンテナイメージです。

主な対象は次のとおりです。

- audit-workerのVaultSendイメージ
- 同じCompose定義で起動する運用サービスの再適用

次は巻き戻しません。

- PostgreSQL schema
- PostgreSQL内の業務データ
- S3 object
- 外部メール・SQS・Stripe等の副作用
- Secret
- Compose volume内のPrometheus・Grafanaデータ

DB migrationやデータ形式が旧イメージと互換でない場合、ロールバックしてはいけません。変更管理・障害対応手順で互換性を確認します。

## 3. 採用構成

1. 通常の承認済みデプロイ成功時に本番hostのrelease台帳を更新
2. 障害時に台帳から戻し元と戻し先を確認
3. GitHub Actionsの`Production Rollback Authorization`を`main`から起動
4. GitHub-hosted runnerで戻し元・戻し先を検証
5. `production-rollback` Environmentで人手承認
6. 承認後に同じfrom/toを再検証
7. 30分有効なrollback許可manifestを生成
8. manifestへGitHub Artifact Attestationを付与
9. 本番hostで許可証、Workflow run、release台帳、実稼働imageを再検証
10. 未使用の許可証で一度だけComposeを起動
11. 成功時にrollback eventとcurrent releaseを台帳へ記録

本番hostはGitHub Actionsのself-hosted runnerとして登録しません。

## 4. 本番release台帳

### 4.1 推奨配置

```text
/var/lib/vaultsend/releases/
├── current-release.json
├── deployment.lock
└── events/
    ├── <通常デプロイ許可manifest SHA-256>.json
    └── <rollback許可manifest SHA-256>.json
```

本番実行ユーザーだけが読み書きできる`700`で管理します。各JSONとlock fileは`600`です。

Git、`/tmp`、共有home、コンテナ内の一時filesystemには置きません。バックアップ対象に含めます。

### 4.2 event内容

成功eventには次を記録します。

- event ID
- `deployment`または`rollback`
- 対象image・source revision
- 直前のevent ID・image・revision
- 許可manifest SHA-256
- 変更管理番号
- Workflow run ID・attempt
- 実行日時
- 本番hostの実行ユーザー

理由本文は記録せず、許可manifest側の理由SHA-256と変更管理番号で関連付けます。

### 4.3 整合性

`scripts/manage-production-release-ledger.sh`は次を検証します。

- event/currentが通常ファイルでありsymlinkではない
- JSON schemaとdigest/revision形式
- current releaseに対応するeventが存在する
- current releaseと最新成功eventが完全一致する
- 新eventのpreviousが現在releaseと一致する
- rollback対象が過去の成功eventに存在する

検証コマンド:

```bash
export PRODUCTION_RELEASE_LEDGER_DIR='/var/lib/vaultsend/releases'
make check-production-release-ledger
```

不整合時は通常デプロイ・rollbackの両方を停止します。台帳ファイルを削除・書換して処理を継続してはいけません。

### 4.4 初期化

台帳を手動で初期化しません。

本機能導入後、通常の`Production Deployment Authorization`で現在の検証済みreleaseを一度デプロイし、成功時に台帳を初期化します。

`current-release.json`がない状態ではrollbackを拒否します。

## 5. 通常デプロイとの連携

通常デプロイ時は次の2台帳を指定します。

```bash
export PRODUCTION_AUTHORIZATION_LEDGER_DIR='/var/lib/vaultsend/deployment-authorizations'
export PRODUCTION_RELEASE_LEDGER_DIR='/var/lib/vaultsend/releases'
make deploy-approved-production
```

通常デプロイ成功後、release eventと`current-release.json`を更新してから許可証を`used`へ確定します。

Compose起動後にrelease台帳更新が失敗した場合は、許可証の`started`記録を残して停止します。同じ許可証は再利用できません。実稼働状態と台帳を調査し、新しい承認で復旧します。

## 6. ロールバックWorkflow

`.github/workflows/production-rollback.yml`を使用します。

実行eventは`workflow_dispatch`だけです。

### 6.1 入力

| 入力 | 要件 |
|---|---|
| `change_request_id` | 3〜80文字の英数字・`._:/-` |
| `rollback_reason` | 10〜300文字、改行・制御文字・機密情報なし |
| `expected_current_image` | 公式GHCRのSHA-256 digest固定参照 |
| `expected_current_revision` | 40桁commit SHA |
| `target_image` | 公式GHCRのSHA-256 digest固定参照 |
| `target_revision` | 40桁commit SHA |
| `confirmation` | `ROLLBACK_PRODUCTION`と完全一致 |

戻し元と戻し先は異なる必要があります。tag指定は拒否します。

### 6.2 申請値の取得

現在release:

```bash
jq '{event_id, image: .target.image, source_revision: .target.source_revision}' \
  /var/lib/vaultsend/releases/current-release.json
```

過去の成功release:

```bash
jq -s '
  sort_by(.completed_at_epoch_ms) |
  map({
    event_id,
    operation,
    image: .target.image,
    source_revision: .target.source_revision,
    completed_at,
    change_request_id: .authorization.change_request_id
  })
' /var/lib/vaultsend/releases/events/*.json
```

候補はこの履歴から選びます。履歴にないdigestを入力しても本番hostで拒否します。

## 7. 承認前検証

`prepare` JobはGitHub-hosted runnerで次を確認します。

1. `main`からの手動起動
2. 入力形式・確認文字列
3. 戻し元imageのCosign署名・provenance・SPDX SBOM
4. 戻し先imageのCosign署名・provenance・SPDX SBOM
5. 両revisionが`main`のfirst-parent履歴に存在する
6. 戻し先revisionが戻し元revisionの祖先
7. from/toと検証結果をArtifactへ保存

この段階では本番hostへ接続しません。

## 8. `production-rollback` Environment

Repository Settingsの`Environments`から`production-rollback`を作成します。

必須設定:

- Required reviewers: 1名以上
- Prevent self-review: 有効
- Deployment branches and tags: `main`だけ
- Allow administrators to bypass protection rules: 無効

Environment variable:

| 名前 | 値 |
|---|---|
| `PRODUCTION_ROLLBACK_ENABLED` | `true` |

Environment secret:

| 名前 | 要件 |
|---|---|
| `PRODUCTION_ROLLBACK_GUARD` | 32文字以上のランダム値 |

同名のrepository variable・repository secretは作成しません。Environment側だけに設定します。

空のEnvironmentが自動作成された場合、変数とGuardがないため許可証発行前にfail-closedで停止します。

## 9. 承認後の許可証

承認後、`authorize` Jobは次を実行します。

1. Environment変数・Guardを検証
2. event、main ref、Workflow refを検証
3. 申請入力を再検証
4. 戻し元・戻し先imageを再検証
5. revision祖先関係を再検証
6. 30分有効なrollback許可manifestを生成
7. manifestへGitHub Artifact Attestationを付与
8. Attestationを即時検証
9. 90日保持Artifactへ保存

Artifact名:

```text
production-rollback-authorization-<run_id>-<run_attempt>
```

manifestには次を含めます。

- rollback種別・approved状態
- `production-rollback`環境
- expected current image/revision
- target image/revision
- 変更管理番号
- 理由SHA-256
- 起動者
- Workflow repository/ref/SHA/run ID/attempt
- 許可日時・失効日時

## 10. 本番hostでの確認・実行

### 10.1 Artifact取得

```bash
gh run download <run-id> \
  --repo mizzz-ivr/vaultsend \
  --name production-rollback-authorization-<run-id>-<attempt> \
  --dir /secure/vaultsend-rollback/<run-id>
```

### 10.2 事前確認

```bash
export PRODUCTION_ROLLBACK_MANIFEST='/secure/vaultsend-rollback/<run-id>/artifacts/production-rollback/authorization/rollback-authorization-manifest.json'
export PRODUCTION_RELEASE_LEDGER_DIR='/var/lib/vaultsend/releases'
make check-approved-rollback
```

次を再検証します。

- manifest schema・rollback種別・approved状態
- 30分以内の有効期限
- Artifact Attestation・Workflow identity
- Workflow runが`workflow_dispatch`・main・success
- current台帳と許可証の戻し元が一致
- 戻し先が過去の成功eventに存在
- 実稼働audit-worker imageとcurrent台帳が一致
- 両revisionのfirst-parent履歴・祖先関係
- 戻し元・戻し先imageの署名・provenance・SPDX SBOM
- Compose設定

`--check`では起動しません。

### 10.3 実行

```bash
export PRODUCTION_ROLLBACK_AUTHORIZATION_LEDGER_DIR='/var/lib/vaultsend/rollback-authorizations'
make rollback-approved-production
```

処理順:

1. release台帳を検証
2. host全体の`deployment.lock`を取得
3. current・実稼働image・target履歴を再検証
4. 許可証の`started`記録を永続化
5. 戻し先digestでCompose起動
6. rollback成功eventをrelease台帳へ追加
7. `current-release.json`を戻し先へ更新
8. 許可証を`used`へ更新

## 11. 一度限り利用と失敗時挙動

ロールバック許可証台帳:

```text
/var/lib/vaultsend/rollback-authorizations
```

- 成功: `<manifest-sha>.used.json`
- 開始後失敗: `<manifest-sha>.started.json`

`started`または`used`がある許可証は再利用できません。

Compose起動途中で失敗した場合も同じ許可証を再利用しません。現在のcontainer状態とrelease台帳を確認し、新しい変更管理番号・新しいEnvironment承認で許可証を再発行します。

通常デプロイとrollbackは同じrelease台帳lockを使用するため、host上で同時実行できません。

## 12. 受け入れテスト

### 正常系

1. 通常の承認済みデプロイでrelease台帳を初期化
2. もう1つ新しいreleaseを正常デプロイして履歴を2件以上にする
3. 起動者Aが過去releaseを指定してrollback Workflowを開始
4. `prepare`が成功
5. `authorize`がWaitingになる
6. 承認者Bが承認
7. Attestation付き許可証が生成される
8. 本番hostの`check-approved-rollback`が成功
9. `rollback-approved-production`が成功
10. current releaseがtargetへ変わる
11. rollback eventと`used`記録が残る
12. 同じ許可証の2回目が拒否される

### 異常系

- tag指定を拒否
- 別repositoryを拒否
- 戻し元と戻し先が同一なら拒否
- target revisionが祖先でなければ拒否
- Environment未設定なら許可証発行前に拒否
- 期限切れ許可証を拒否
- Attestation不一致を拒否
- Workflow run不一致を拒否
- current台帳不一致を拒否
- 実稼働image不一致を拒否
- 正常デプロイ履歴にないtargetを拒否
- 台帳不整合を拒否
- 使用済み許可証を拒否
- Compose失敗後の同一許可証再利用を拒否

## 13. セキュリティ・運用上の注意

- Docker操作権限を持つ利用者は本番host上の強い権限を持ちます
- `deploy-verified-compose.sh`を直接実行できるOS権限はsudo・所有権・運用規程で制限します
- release台帳と許可証台帳を削除・改変して処理を継続しません
- GitHub Actions入力へ機密情報・個人情報・tokenを記載しません
- ArtifactはWORM保管ではありません
- GHCR、GitHub API、Sigstoreへのオンライン到達性が必要です
- 30分以内でも、承認後に本番状態が変わればcurrent一致検証で拒否します
- rollback後は原因調査、forward fix、監視確認、変更記録更新を行います

## 14. 未対応事項

1. PostgreSQL data/schema rollback
2. S3 object復旧
3. ロールバック後の自動synthetic check
4. 複数host・クラスタへの整合的rollback
5. release台帳・許可証・結果のWORM保管
6. Environment設定の定期監査
7. 本番hostのsudo・Docker権限監査
8. 自動障害判定・無人rollback
