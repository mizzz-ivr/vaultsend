# 本番デプロイ承認・実行分離

## 1. 目的

VaultSendの本番Composeデプロイについて、以下を分離します。

- デプロイ要求を起動する人
- デプロイ内容を承認する人
- 本番hostで処理を実行するrunner

署名・provenance・SPDX SBOMを検証できた現在の`main`イメージだけを対象とし、未承認・任意digest・任意branch・想定外runnerからの本番反映を拒否します。

## 2. Workflow

`.github/workflows/production-deploy.yml`を使用します。

実行eventは`workflow_dispatch`だけです。`push`、`pull_request`、`schedule`から本番デプロイは開始しません。

### 入力

| 入力 | 要件 |
|---|---|
| `change_request_id` | 3〜80文字の英数字・`._:/-` |
| `deployment_reason` | 10〜300文字、改行・制御文字なし |
| `confirmation` | `DEPLOY_PRODUCTION`と完全一致 |

イメージ、digest、revisionは入力できません。Workflowが現在公開中の`ghcr.io/mizzz-ivr/vaultsend:main`を取得し、正規digestと公開commitを決定します。

## 3. 処理の流れ

### 3.1 承認前

GitHub-hosted runnerで以下を実行します。

1. 起動branchが`main`であることを確認
2. 変更管理番号・理由・確認文字列を検証
3. `main`タグをread-onlyでpull
4. RepoDigestから正規のSHA-256 digest参照を抽出
5. OCI source・revisionを確認
6. Supply Chain公開対象pathの最新`main` first-parent commitとrevisionを照合
7. Cosign署名を検証
8. GitHub build provenanceを検証
9. SPDX SBOM Attestationを検証
10. 承認対象manifestをArtifactへ保存

この段階では本番hostへ接続せず、Compose状態を変更しません。

### 3.2 Environment承認

`deploy` Jobは`production` Environmentを参照します。

Environmentの保護ルールが通るまで、Jobはself-hosted runnerへ割り当てられません。必須レビューを設定した場合、承認されるまでJobはWaitingになります。

### 3.3 承認後

専用self-hosted runnerで以下を実行します。

1. Environment有効化フラグ・Guard Secret・期待runner名を確認
2. `workflow_dispatch`、`main`、正規Workflow refを確認
3. runnerが非rootであることを確認
4. Docker・Cosign・GitHub CLI・jqなどの前提コマンドを確認
5. `deploy/compose/.env`が通常ファイルで、group/other書込み不可であることを確認
6. 承認前と同じdigest・revisionを再検証
7. `deploy-verified-compose.sh --deploy`でComposeを起動
8. 4サービスがrunningになるまで最大60秒確認
9. デプロイ前後のCompose状態と結果manifestをArtifactへ保存

## 4. `production` Environmentの必須設定

Repository Settingsの`Environments`から`production`を作成します。

### 保護ルール

- Required reviewers: 1名以上
- Prevent self-review: 有効
- Deployment branches and tags: `main`だけ
- Allow administrators to bypass protection rules: 無効

起動者と承認者を同一にしないため、自己承認禁止を有効化します。リポジトリに実質1人しか参加していない場合は、read権限以上を持つ別ユーザーまたはチームを承認者として追加する必要があります。

### Environment variables

| 名前 | 値 |
|---|---|
| `PRODUCTION_DEPLOYMENT_ENABLED` | `true` |
| `PRODUCTION_RUNNER_NAME` | 専用runnerのGitHub表示名と完全一致 |

### Environment secret

| 名前 | 要件 |
|---|---|
| `PRODUCTION_ENVIRONMENT_GUARD` | 32文字以上のランダム値 |

GuardはアプリケーションSecretではありません。Environmentが意図的に構成・有効化されたことを確認し、保護なしで自動作成された空のEnvironmentからの実行を拒否するために使用します。

同名のrepository variable・repository secretは作成しません。Environment側にだけ設定します。

## 5. self-hosted runner

### runnerの範囲

- Repository-level runnerとして登録
- 組織内の他repositoryと共有しない
- 本番hostまたは本番host専用の管理hostで稼働
- カスタムlabelとして`vaultsend-production`を追加
- Workflowが要求するlabel: `self-hosted`, `linux`, `x64`, `vaultsend-production`

### 実行ユーザー

- rootでrunner serviceを起動しない
- 専用OSユーザーを使用
- rootless Dockerを推奨
- Docker socketを使用する場合、Docker操作権限は実質的なhost管理権限として扱う
- 対話ログイン、shell履歴、不要なsudo権限を付与しない

### ネットワーク

runnerから以下へのoutbound通信を許可します。

- GitHub Actions
- GitHub API
- GHCR
- Sigstore trusted root・transparency log

外部からrunnerへ接続するための受信ポートは原則として開放しません。

### 必須コマンド

- Docker Engine
- Docker Compose v2
- GitHub CLI
- jq
- coreutils (`date`, `sha256sum`, `sort`, `stat`)

CosignはWorkflow内のcommit SHA固定Actionでセットアップします。

## 6. 本番hostの事前準備

以下を準備します。

- `deploy/compose/.env`
- audit-worker用DB接続Secretファイル
- internal metrics tokenファイル
- Alertmanager webhook URLファイル
- Grafana管理者パスワードファイル
- `vaultsend-monitoring` external network

`deploy/compose/.env`はsymbolic linkにせず、group/otherへ書込み権限を付与しません。推奨modeは`600`または`640`です。

Secret本文をGitHub Environmentへコピーする必要はありません。本番host上のSecretファイルをCompose Secretとして参照します。

## 7. 実行手順

1. GitHubのActionsを開く
2. `Production Deployment`を選択
3. `Run workflow`でbranchを`main`にする
4. 変更管理番号を入力
5. 1行のデプロイ理由を入力
6. `DEPLOY_PRODUCTION`を入力
7. Workflowを開始
8. `prepare`のArtifactとJob Summaryを確認
9. 起動者とは別のrequired reviewerが`production`を承認
10. 専用runnerでデプロイが完了することを確認

承認者は次を確認します。

- 変更管理番号が実在する
- 対象digestとrevisionが意図したリリースである
- Cosign・provenance・SPDX SBOMが成功している
- 監視・変更時間帯・影響範囲が妥当である
- rollback手順または復旧担当が確認できる

## 8. Artifact

### 承認前

```text
production-deployment-preflight-<run_id>-<run_attempt>
```

主な内容:

- 要求JSON
- release解決結果
- 署名・Attestation検証結果
- approval manifest

### 承認後

```text
production-deployment-result-<run_id>-<run_attempt>
```

主な内容:

- 本番host上の再検証結果
- デプロイ前Compose状態
- デプロイ後Compose状態
- デプロイ結果manifest

保持期間は90日です。より長い保管が必要な場合は、組織の監査保管先へ移送します。

GitHub上のEnvironment approval履歴を、変更管理番号とWorkflow run IDで関連付けて保管します。

## 9. 受け入れテスト

### 承認成功

1. 起動者AがWorkflowを開始
2. `prepare`が成功
3. `deploy`がWaitingになる
4. 承認者Bが承認
5. 専用runnerでデプロイ成功
6. 2種類のArtifactが保存される

### 自己承認拒否

1. 起動者AがWorkflowを開始
2. 起動者Aが承認できないことを確認

### 却下

1. 承認者BがReject
2. `deploy`が実行されずWorkflowが失敗することを確認

### Environment未設定

- 有効化変数、Guard Secret、期待runner名のいずれかを欠落させる
- Compose起動前に失敗することを確認

### 想定外runner

- 同じlabelを持つ別runnerへ割り当てられた場合、`PRODUCTION_RUNNER_NAME`不一致で失敗することを確認

## 10. リスク・制約

- GitHub Environmentの保護ルールはrepository設定であり、WorkflowファイルだけではRequired reviewersや自己承認禁止を設定できません
- Environment設定APIにはrepository Administration write権限が必要です
- self-hosted runnerはGitHub-hosted runnerと同じ隔離境界ではありません
- Docker操作権限を持つrunner利用者はhost上で強い権限を持ちます
- ArtifactはWORM保管ではありません
- 現在のWorkflowは最新の検証済み`main`イメージだけをデプロイし、過去digestへのrollbackは扱いません
- 自動rollbackは行いません。途中失敗時はCompose状態と監視を確認し、承認済みの復旧手順を実行します

## 11. 後続課題

1. rollback用digest台帳と承認Workflow
2. GitHub Environment設定の定期監査
3. Artifactの長期WORM保管
4. runnerの短命化またはジョブ単位再登録
5. 本番デプロイ後のAPI・監視synthetic check
6. release tag向け承認policy
