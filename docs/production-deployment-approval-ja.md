# 本番デプロイ承認・実行分離

## 1. 目的

VaultSendの本番Composeデプロイについて、次の役割を分離します。

- デプロイ要求を起動する人
- `production` Environmentで内容を承認する人
- 本番hostで許可証を検証して実行する人

本番hostはGitHub Actionsのself-hosted runnerとして登録しません。Environment承認後にGitHub-hosted runnerが発行する、2時間で失効するArtifact Attestation付き許可証だけを公式なデプロイ経路で受け入れます。

通常デプロイ成功時には、本番hostのrelease台帳へ成功eventとcurrent releaseを記録します。この台帳は後続の承認済みロールバックで利用します。

## 2. 採用理由

public repositoryのself-hosted runnerを本番hostへ常駐させると、同一repository内の別Workflowがrunner labelを指定し、本番host上でコードを実行する余地が残ります。

そのため、次の構成を採用します。

1. GitHub-hosted runnerで公開イメージを事前検証
2. `production` Environmentで人手承認
3. GitHub-hosted runnerで同一digestを再検証
4. 期限付き許可manifestを生成
5. manifestへGitHub Artifact Attestationを付与
6. 本番hostでmanifest・Attestation・Workflow run・イメージを再検証
7. 未使用の許可証で一度だけComposeを起動
8. 成功したreleaseを本番hostの台帳へ記録

## 3. Workflow

`.github/workflows/production-deploy.yml`を使用します。

実行eventは`workflow_dispatch`だけです。`push`、`pull_request`、`schedule`、`workflow_run`から許可証は発行しません。

### 入力

| 入力 | 要件 |
|---|---|
| `change_request_id` | 3〜80文字の英数字・`._:/-` |
| `deployment_reason` | 10〜300文字、改行・制御文字なし、機密情報禁止 |
| `confirmation` | `DEPLOY_PRODUCTION`と完全一致 |

イメージ、digest、revisionは入力できません。Workflowが現在公開中の`ghcr.io/mizzz-ivr/vaultsend:main`から正規digestと公開commitを決定します。

## 4. 承認前の事前検証

`prepare` JobはGitHub-hosted runnerで次を実行します。

1. 起動refが`refs/heads/main`であることを確認
2. 変更管理番号・理由・確認文字列を検証
3. GHCRへread-onlyでログイン
4. `main`タグをRepoDigestへ解決
5. OCI source・revisionを確認
6. Supply Chain公開対象pathの最新`main` first-parent commitとrevisionを照合
7. Cosign署名を検証
8. GitHub build provenanceを検証
9. SPDX SBOM Attestationを検証
10. 承認対象JSONと証跡をArtifactへ保存

この段階では本番hostへ接続せず、Compose状態を変更しません。

## 5. `production` Environment

`authorize` Jobは`production` Environmentを参照します。

Repository Settingsの`Environments`から`production`を作成し、次を設定します。

- Required reviewers: 1名以上
- Prevent self-review: 有効
- Deployment branches and tags: `main`だけ
- Allow administrators to bypass protection rules: 無効

Environment variable:

| 名前 | 値 |
|---|---|
| `PRODUCTION_DEPLOYMENT_ENABLED` | `true` |

Environment secret:

| 名前 | 要件 |
|---|---|
| `PRODUCTION_ENVIRONMENT_GUARD` | 32文字以上のランダム値 |

同名のrepository variable・repository secretは作成しません。Environment側だけに設定します。

変数またはGuardが欠落している場合、承認後も許可証発行前にfail-closedで停止します。

## 6. 承認後の許可証発行

承認後、`authorize` Jobは次を実行します。

1. Environment有効化変数とGuard Secretを確認
2. event、main ref、Workflow refを確認
3. デプロイ要求を再検証
4. 承認待ちの間に公開digest・revisionが変わっていないことを確認
5. 同じdigestのCosign署名・provenance・SPDX SBOMを再検証
6. 2時間で失効する`authorization-manifest.json`を生成
7. `actions/attest`でmanifestへArtifact Attestationを付与
8. Attestationを同じJob内で即時検証
9. manifestと検証証跡をArtifactへ保存

許可manifestには次を含めます。

- schema version
- `approved`状態
- `production`環境
- digest固定イメージ
- source revision
- 変更管理番号
- デプロイ理由SHA-256
- 起動者
- repository・Workflow ref・Workflow SHA
- Workflow run ID・attempt
- 許可日時・失効日時

理由本文はArtifactとJob Summaryへ保存しません。

## 7. 許可証Artifact

Artifact名:

```text
production-deployment-authorization-<run_id>-<run_attempt>
```

保持期間は90日です。

```text
artifacts/production-deployment/authorization/
├── authorization-manifest.json
├── authorization-manifest.json.sha256
├── authorization-attestation-verification.json
├── request/request.json
├── release/release-resolution.json
└── release-verification/
```

ArtifactはWORM保管ではありません。長期監査が必要な場合は組織の監査保管先へ移送します。

## 8. 本番hostの前提

必要なツール:

- Docker Engine・Docker Compose v2
- Cosign
- GitHub CLI
- jq
- coreutils
- util-linuxの`flock`

必要な運用ファイル:

- `deploy/compose/.env`
- audit-worker用DB接続Secret
- internal metrics token
- Alertmanager webhook URL
- Grafana管理者パスワード
- `vaultsend-monitoring` external network

### 永続台帳

```text
/var/lib/vaultsend/deployment-authorizations
/var/lib/vaultsend/releases
```

- deployment authorizations: 許可証の`started`・`used`記録
- releases: 成功した通常デプロイ・rollbackの履歴とcurrent release

本番実行ユーザーだけが読み書きできる`700`で管理します。Git、`/tmp`、共有ディレクトリには置きません。

release台帳の詳細は[本番ロールバック承認・release台帳・実行手順](./production-rollback-ja.md)を参照します。

## 9. 実行手順

### 9.1 許可申請

1. GitHub Actionsで`Production Deployment Authorization`を開く
2. branchを`main`にする
3. 変更管理番号を入力
4. 機密情報を含まない1行の理由を入力
5. `DEPLOY_PRODUCTION`を入力
6. Workflowを開始
7. `prepare`のJob SummaryとArtifactを確認
8. 起動者とは別のreviewerが`production`を承認
9. `authorize`が成功することを確認

承認者は変更管理番号、digest、revision、検証結果、作業時間、監視・復旧担当を確認します。

### 9.2 Artifact取得

```bash
gh run download <run-id> \
  --repo mizzz-ivr/vaultsend \
  --name production-deployment-authorization-<run-id>-<attempt> \
  --dir /secure/vaultsend-deployment/<run-id>
```

### 9.3 デプロイ前確認

```bash
export PRODUCTION_AUTHORIZATION_MANIFEST='/secure/vaultsend-deployment/<run-id>/artifacts/production-deployment/authorization/authorization-manifest.json'
make check-approved-production
```

再検証対象:

- manifest schema・approved状態・production環境
- Workflow identity・main ref・Workflow SHA
- 2時間以内の有効期限
- manifestのArtifact Attestation
- Workflow runが`workflow_dispatch`・main・success
- digest固定イメージのCosign署名
- build provenance
- SPDX SBOM
- Compose設定

`--check`では起動しません。

### 9.4 本番デプロイ

```bash
export PRODUCTION_AUTHORIZATION_LEDGER_DIR='/var/lib/vaultsend/deployment-authorizations'
export PRODUCTION_RELEASE_LEDGER_DIR='/var/lib/vaultsend/releases'
make deploy-approved-production
```

処理順:

1. release台帳の整合性を検証
2. 通常デプロイ・rollback共通のglobal lockを取得
3. 許可証の使用開始記録を永続化
4. 署名検証済みdigestでComposeを起動
5. 成功release eventを記録
6. `current-release.json`を更新
7. 許可証を`used`へ更新

成功時:

- `<manifest-sha>.used.json`
- `/var/lib/vaultsend/releases/events/<manifest-sha>.json`
- `/var/lib/vaultsend/releases/current-release.json`

途中失敗時は許可証の`started`記録を残し、同じ許可証を再利用しません。

## 10. release台帳の初期化・確認

release台帳は手動で初期化しません。

本機能導入後、通常の承認済みデプロイを一度成功させて初期化します。

```bash
export PRODUCTION_RELEASE_LEDGER_DIR='/var/lib/vaultsend/releases'
make check-production-release-ledger
jq . /var/lib/vaultsend/releases/current-release.json
```

台帳不整合時はデプロイ・rollbackの両方を停止します。ファイルを削除・改変して継続しません。

## 11. 受け入れテスト

### 承認成功

1. 起動者AがWorkflowを開始
2. `prepare`が成功
3. `authorize`がWaitingになる
4. 承認者Bが承認
5. Attestation付き許可証が発行される
6. 本番hostで`check-approved-production`が成功
7. `deploy-approved-production`が成功
8. 許可証台帳に`used`が残る
9. release eventとcurrent releaseが記録される

### 拒否系

- 自己承認を拒否
- reviewerがRejectした場合はmanifestを生成しない
- Environment変数・Guard欠落時は許可証発行前に拒否
- 期限切れmanifestを拒否
- 使用済みmanifestの2回目を拒否
- Compose失敗後の同一manifest再利用を拒否
- release台帳不整合時に拒否
- 同時デプロイ・rollbackをglobal lockで拒否

## 12. ロールバック

過去digestを通常デプロイWorkflowへ入力することはできません。

承認済みロールバックは専用の`Production Rollback Authorization`と`production-rollback` Environmentを使用します。

手順と制約は[本番ロールバック承認・release台帳・実行手順](./production-rollback-ja.md)を参照します。

ロールバックはコンテナイメージだけを戻し、DB schema・データ・S3を巻き戻しません。

## 13. リスク・制約

- Environment保護ルールはrepository設定であり、コードだけでは有効性を証明できません
- GitHub、GHCR、Sigstoreへオンライン到達できる必要があります
- Docker操作権限を持つ利用者は本番host上の強い権限を持ちます
- 既存scriptを直接実行できるOS権限はsudo・所有権・運用規程で制限します
- 許可証・release台帳・証跡ArtifactはWORM保管ではありません
- Compose成功後の台帳更新失敗時は手動調査が必要です

## 14. 後続課題

1. Environment設定の定期監査
2. 許可証・release台帳・デプロイ証跡の長期WORM保管
3. 本番デプロイ・rollback後のAPI・監視synthetic check
4. 本番hostのsudo・Docker権限監査
5. release tag向け承認policy
6. PostgreSQL・S3・業務データの復旧手順
