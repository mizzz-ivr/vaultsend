# 公開済みリリースイメージの継続検証

## 1. 目的

GHCRへ公開されたVaultSendの`main`イメージについて、公開時だけでなく継続的に以下を確認します。

- `main`タグが正規のSHA-256 digestへ解決できる
- OCI source・revisionが期待するGitHub repositoryと公開対象commitに一致する
- Cosign keyless署名が正規のSupply Chain Workflow由来である
- GitHub build provenanceが同じsource commitを示す
- SPDX SBOM Attestationが同じdigestへ関連付いている
- self-hosted runner由来のAttestationを受け入れない

署名・Attestationを生成できても、registry上のイメージを実際に取得してデプロイpolicyで再検証できなければ、公開経路の受け入れ確認は完了しません。

## 2. Workflow

`.github/workflows/release-image-acceptance.yml`を使用します。

### 実行条件

| 条件 | 目的 |
|---|---|
| Supply Chain Workflowの`main`向けpush実行が成功 | 公開直後の自動受け入れ確認 |
| 毎日03:15 JST | package削除、署名・Attestation不整合、認証・trusted root問題の継続検知 |
| `workflow_dispatch` | 障害対応・監査時の手動再検証 |
| 同一repositoryのPull Request | 検証script・Workflow変更時に、現在公開中の実イメージで回帰確認 |

Fork Pull Requestでは、検証コードへpackage・Attestationのread権限を渡さないため、オンライン検証Jobを実行しません。

## 3. 権限

Workflow全体はread-onlyです。

- `contents: read`
- `packages: read`
- `attestations: read`

GHCRへのpush、OIDC token発行、署名、Attestation登録、デプロイ操作は行いません。

## 4. 検証対象commitの決定

### 公開直後

`workflow_run.head_sha`を期待commitとして使用します。Supply Chain Workflowが公開したcommitとOCI revisionが完全一致しなければ失敗します。

### 定期・手動・Pull Request

単純な`main` HEADではなく、Supply Chain Workflowの公開対象pathを最後に変更したcommitを算出します。

対象path:

```text
Dockerfile
.dockerignore
.gitignore
go.mod
go.sum
cmd/
internal/
web/package.json
web/package-lock.json
scripts/verify-supply-chain.sh
.github/workflows/supply-chain.yml
.github/dependabot.yml
```

文書だけの変更でイメージを再公開していない場合に誤検知せず、コンテナ内容へ影響する変更後に公開が行われていない場合は不一致として検出します。

Supply Chain Workflowの`push.paths`を変更する場合は、この一覧も同じPull Requestで更新します。

## 5. 検証手順

1. `ghcr.io/mizzz-ivr/vaultsend:main`をread-only tokenでpull
2. DockerのRepoDigestsから公式repositoryのdigest参照を抽出
3. digest形式を厳格検証
4. 期待する公開対象commitを算出
5. Cosignを検証済みAction SHAから導入
6. `scripts/verify-release-image.sh`へdigestと期待commitを渡す
7. Cosign署名、provenance、SPDX SBOM、OCI metadataを検証
8. Job SummaryとArtifactへ結果を保存

タグはdigest解決の入口にのみ使用し、署名・Attestation検証は必ずdigest参照に対して実行します。

## 6. 検証証跡

Artifact名:

```text
release-image-acceptance-<run_id>-<run_attempt>
```

保存内容:

```text
release-image-reference.txt
release-expected-revision.txt
artifacts/release-acceptance/
├── requested-image.txt
├── expected-source-revision.txt
├── image-inspect.json
├── source-revision.txt
├── cosign-verification.json
├── provenance-verification.json
├── sbom-verification.json
└── verification-summary.json
```

保持期間は90日です。リリース承認記録やデプロイ記録を90日より長く保管する必要がある場合は、組織の監査保管先へ移送します。

## 7. 失敗時の扱い

### `main`タグをpullできない

- GHCR packageの存在、visibility、repositoryとの関連付けを確認
- Workflow tokenの`packages: read`を確認
- package削除・retention policy・障害情報を確認

### OCI revision不一致

- Supply Chain Workflowの対象runとhead SHAを確認
- `main`タグが別digestへ上書きされていないか確認
- 公開対象path変更後にpublish Jobが成功しているか確認
- 不一致イメージをデプロイしない

### Cosign検証失敗

- certificate identityとOIDC issuerを確認
- 対象digestを正規Workflowから再buildする
- 失敗したdigestへ署名だけを後付けして正規品扱いにしない

### provenance・SPDX SBOM失敗

- GitHub Artifact Attestationの登録結果を確認
- source ref、source digest、signer workflowを確認
- SBOM生成・登録stepを確認
- 証跡が揃うまでデプロイしない

## 8. ローカル・デプロイhostでの確認

GitHub Actionsの成功は、デプロイhost固有のDocker・network・credential状態を保証しません。本番反映前には同じdigestで以下を実行します。

```bash
export VAULTSEND_IMAGE='ghcr.io/mizzz-ivr/vaultsend@sha256:<digest>'
export EXPECTED_SOURCE_REVISION='<40桁の公開commit SHA>'
make check-operations-deploy
```

本番デプロイは、上記成功後に同じdigestを指定して`make deploy-operations`を実行します。

## 9. リスク・制約

- GitHub Actions、GHCR、GitHub API、Sigstore trusted rootへオンライン到達できる必要がある
- 定期Workflowの遅延は即時の改ざん検知を保証しない
- Docker daemon権限を持つ利用者はhost上でwrapperを迂回できる
- ArtifactはWORM保管ではない
- release tag向けidentity policyは別途設計が必要
- ECS・Kubernetesへ移行する場合は同じpolicyを基盤側へ移植する

## 10. 後続課題

1. デプロイ承認者と実行者の職務分離
2. GitHub Environmentによる本番承認ゲート
3. 検証証跡の長期WORM保管
4. rollback用digest台帳
5. release tag向けidentity policy
6. registry retention policy
