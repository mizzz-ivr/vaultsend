# 本番デプロイ承認・実行分離

## 1. 目的

VaultSendの本番Composeデプロイについて、次の役割を分離します。

- デプロイ要求を起動する人
- `production` Environmentで内容を承認する人
- 本番hostで許可証を検証して実行する人

本番hostはGitHub Actionsのself-hosted runnerとして登録しません。Environment承認後にGitHub-hosted runnerが発行する、2時間で失効するArtifact Attestation付き許可証だけを公式なデプロイ経路で受け入れます。

## 2. 採用理由

public repositoryのself-hosted runnerを本番hostへ常駐させると、同一repository内の別Workflowがrunner labelを直接指定し、本番host上でコードを実行する余地が残ります。

GitHub EnvironmentはEnvironmentを参照するJobを保護しますが、Environmentを参照しない別Workflowまでrunner側で強制的に拒否する仕組みではありません。

そのため、以下の構成を採用します。

1. GitHub-hosted runnerで公開イメージを事前検証
2. `production` Environmentで人手承認
3. GitHub-hosted runnerで同一digestを再検証
4. 期限付き許可manifestを生成
5. manifestへGitHub Artifact Attestationを付与
6. 本番hostでmanifest・Attestation・Workflow run・イメージを再検証
7. 未使用の許可証で一度だけComposeを起動

## 3. Workflow

`.github/workflows/production-deploy.yml`を使用します。

実行eventは`workflow_dispatch`だけです。`push`、`pull_request`、`schedule`、`workflow_run`から許可証は発行しません。

### 入力

| 入力 | 要件 |
|---|---|
| `change_request_id` | 3〜80文字の英数字・`._:/-` |
| `deployment_reason` | 10〜300文字、改行・制御文字なし |
| `confirmation` | `DEPLOY_PRODUCTION`と完全一致 |

イメージ、digest、revisionは入力できません。Workflowが現在公開中の`ghcr.io/mizzz-ivr/vaultsend:main`から正規digestと公開commitを決定します。

## 4. 承認前の事前検証

`prepare` JobはGitHub-hosted runnerで以下を実行します。

1. 起動refが`refs/heads/main`であることを確認
2. 変更管理番号・理由・確認文字列を検証
3. GHCRへread-onlyでログイン
4. `main`タグをpullしてRepoDigestへ解決
5. OCI source・revisionを確認
6. Supply Chain公開対象pathの最新`main` first-parent commitとrevisionを照合
7. Cosign署名を検証
8. GitHub build provenanceを検証
9. SPDX SBOM Attestationを検証
10. 承認対象JSONと証跡をArtifactへ保存

この段階では本番hostへ接続せず、Compose状態を変更しません。

## 5. `production` Environment

`authorize` Jobは`production` Environmentを参照します。Environmentの保護ルールが満たされるまで、許可証は発行されません。

### 必須設定

Repository Settingsの`Environments`から`production`を作成し、次を設定します。

- Required reviewers: 1名以上
- Prevent self-review: 有効
- Deployment branches and tags: `main`だけ
- Allow administrators to bypass protection rules: 無効

起動者とは別の承認者を設定します。実質的な利用者が1人しかいない場合は、read権限以上を持つ別ユーザーまたはチームを承認者として追加します。

### Environment variable

| 名前 | 値 |
|---|---|
| `PRODUCTION_DEPLOYMENT_ENABLED` | `true` |

### Environment secret

| 名前 | 要件 |
|---|---|
| `PRODUCTION_ENVIRONMENT_GUARD` | 32文字以上のランダム値 |

GuardはアプリケーションSecretではありません。保護ルールなしで自動作成された空のEnvironmentや、意図せず無効化されたEnvironmentから許可証を発行しないためのfail-closed確認値です。

同名のrepository variable・repository secretは作成せず、Environment側だけに設定します。

Environment設定はrepository Administration権限を必要とします。WorkflowファイルだけではRequired reviewers、自己承認禁止、branch制限を設定できません。

## 6. 承認後の許可証発行

承認後、`authorize` JobはGitHub-hosted runnerで次を実行します。

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
- デプロイ理由のSHA-256
- 起動者
- repository・Workflow ref・Workflow SHA
- Workflow run ID・attempt
- 許可日時
- 失効日時

理由本文はJob Summaryへ出力せず、SHA-256だけを表示します。

## 7. 許可証Artifact

Artifact名は次の形式です。

```text
production-deployment-authorization-<run_id>-<run_attempt>
```

保持期間は90日です。主な内容は次のとおりです。

```text
artifacts/production-deployment/authorization/
├── authorization-manifest.json
├── authorization-manifest.json.sha256
├── authorization-attestation-verification.json
├── request/request.json
├── release/release-resolution.json
└── release-verification/
```

Artifact自体はWORM保管ではありません。長期監査が必要な場合は、組織の監査保管先へ移送します。

## 8. 本番hostの前提

本番hostへ以下を導入します。

- Docker Engine・Docker Compose v2
- Cosign
- GitHub CLI
- jq
- coreutils

GitHub CLIは対象repositoryのWorkflow runとAttestationを読み取れる最小権限tokenで認証します。GHCRもread-only credentialを使用します。

以下を準備します。

- `deploy/compose/.env`
- audit-worker用DB接続Secretファイル
- internal metrics tokenファイル
- Alertmanager webhook URLファイル
- Grafana管理者パスワードファイル
- `vaultsend-monitoring` external network
- 永続許可証台帳ディレクトリ

台帳ディレクトリの推奨例:

```text
/var/lib/vaultsend/deployment-authorizations
```

本番実行ユーザーだけが読み書きできる`700`で管理します。Git、`/tmp`、共有ディレクトリには置きません。

## 9. 実行手順

### 9.1 許可申請

1. GitHub Actionsで`Production Deployment Authorization`を開く
2. branchを`main`にする
3. 変更管理番号を入力
4. 1行の理由を入力
5. `DEPLOY_PRODUCTION`を入力
6. Workflowを開始
7. `prepare`のJob SummaryとArtifactを確認
8. 起動者とは別のreviewerが`production`を承認
9. `authorize`が成功することを確認

承認者は次を確認します。

- 変更管理番号が実在する
- digest・revisionが意図したリリースである
- Cosign・provenance・SPDX SBOMが成功している
- 変更時間帯、監視、影響範囲、復旧担当が妥当である

### 9.2 Artifact取得

Workflow run IDとattemptを確認し、安全な作業ディレクトリへ取得します。

```bash
gh run download <run-id> \
  --repo mizzz-ivr/vaultsend \
  --name production-deployment-authorization-<run-id>-<attempt> \
  --dir /secure/vaultsend-deployment/<run-id>
```

許可manifestのpathを確認します。

```bash
find /secure/vaultsend-deployment/<run-id> \
  -name authorization-manifest.json \
  -type f
```

### 9.3 デプロイ前確認

```bash
export PRODUCTION_AUTHORIZATION_MANIFEST='/secure/vaultsend-deployment/<run-id>/artifacts/production-deployment/authorization/authorization-manifest.json'
make check-approved-production
```

次をすべて再検証します。

- manifest JSON形式・schema
- approved状態・production環境
- Workflow identity・main ref・Workflow SHA
- 2時間以内の有効期限
- manifestのArtifact Attestation
- Workflow runが`workflow_dispatch`・main・successであること
- digest固定イメージのCosign署名
- build provenance
- SPDX SBOM
- Compose設定

`--check`ではコンテナを起動しません。

### 9.4 本番デプロイ

```bash
export PRODUCTION_AUTHORIZATION_LEDGER_DIR='/var/lib/vaultsend/deployment-authorizations'
make deploy-approved-production
```

公式scriptはCompose起動前に許可証の使用開始記録を永続化します。

- 成功時: `*.used.json`
- 途中失敗時: `*.started.json`

`started`または`used`が存在する許可証は再利用できません。途中失敗後は状態を調査し、新しい変更管理番号と新しい承認で許可証を再発行します。台帳ファイルを削除して再利用してはいけません。

## 10. 受け入れテスト

### 承認成功

1. 起動者AがWorkflowを開始
2. `prepare`が成功
3. `authorize`がWaitingになる
4. 承認者Bが承認
5. Attestation付き許可証が発行される
6. 本番hostで`check-approved-production`が成功
7. `deploy-approved-production`が成功
8. 台帳に`used`記録が残る

### 自己承認拒否

- 起動者Aが自分の申請を承認できないことを確認

### 却下

- reviewerがRejectした場合、許可manifestが生成されないことを確認

### Environment未設定

- 有効化変数またはGuard Secretが欠落した状態では、承認後も許可証発行前に失敗することを確認

### 期限切れ

- 発行から2時間を超えたmanifestを本番hostが拒否することを確認

### 二重使用

- 使用済みmanifestで2回目のデプロイが拒否されることを確認

### 途中失敗

- Compose起動失敗時に`started`記録が残り、同じmanifestの再利用が拒否されることを確認

## 11. リスク・制約

- Environmentの保護ルールはrepository設定であり、コードレビューだけでは有効性を証明できません
- GitHub、GHCR、Sigstoreへオンライン到達できる必要があります
- 本番hostでDocker操作権限を持つ利用者は強いhost権限を持ちます
- GitHub外から既存の`deploy-verified-compose.sh`を直接実行できるOS権限が残るため、本番hostのsudo・ファイル権限・運用規程も必要です
- 許可証は最新の検証済み`main`イメージだけを対象とします
- rollbackは対象外で、過去digestを入力する経路はありません
- 自動rollbackは行いません

## 12. 後続課題

1. rollback用digest台帳と専用承認Workflow
2. Environment設定の定期監査
3. 許可証・デプロイ証跡の長期WORM保管
4. 本番デプロイ後のAPI・監視synthetic check
5. 本番hostのsudo・Docker権限監査
6. release tag向け承認policy
