# ソフトウェアサプライチェーン セキュリティ運用

## 1. 目的

VaultSendのコンテナイメージについて、以下を継続的に確認できる状態を作ります。

- どのソースコード・Workflow・commitから生成されたか
- イメージにどのOS package・Go module等が含まれるか
- 修正版が提供されている重大脆弱性を含んでいないか
- Secretや危険なIaC設定がリポジトリへ混入していないか
- GHCRへ公開されたdigestがGitHub Actionsから署名されたものか

署名やSBOMだけで行政利用可能になるわけではありません。脆弱性対応SLA、変更管理、例外承認、インシデント対応、デプロイ時の検証まで含めて運用します。

## 2. Workflow

`.github/workflows/supply-chain.yml`を使用します。

### Pull Request

コンテナ関連ファイルやアプリ依存関係が変更された場合に、以下を実行します。

1. runtimeコンテナをbuild
2. 実行userが`65532:65532`であることを確認
3. リポジトリのSecret・misconfiguration scan
4. CycloneDX JSON SBOM生成
5. SPDX JSON SBOM生成
6. High / Critical脆弱性のJSON report生成
7. 修正版が提供されているHigh / Critical脆弱性を品質ゲートとして拒否
8. SBOMと検査結果を30日間Artifactへ保存

PRではGHCRへのpush、OIDC token発行、署名、Attestation登録を行いません。

### main・release tag

`main`への対象変更pushまたは`v*`tagで、以下を実行します。

1. GHCRへログイン
2. linux/amd64 runtime imageをbuild
3. `sha-<commit>`tagと`main`またはrelease tagをpush
4. registryが返したSHA-256 digestを取得
5. push済みdigestを再取得してSBOM・脆弱性検査
6. GitHub Artifact Attestationでbuild provenanceを登録
7. SPDX SBOM Attestationを登録
8. Cosign keyless署名をOCI registryへ追加
9. Cosign署名をOIDC identity条件付きで即時検証
10. GitHub CLIでAttestationを即時検証
11. SBOMと検査結果を90日間Artifactへ保存

公開・署名対象はtagではなくdigestです。デプロイ時も以下の形式を使用します。

```text
ghcr.io/mizzz-ivr/vaultsend@sha256:<digest>
```

## 3. 権限境界

Workflow全体の既定権限は`contents: read`です。

GHCR公開Jobだけ、次の権限を追加します。

- `packages: write`
- `id-token: write`
- `attestations: write`

PR Jobには書込み権限やOIDC権限を付与しません。Fork由来コードからpackage公開やkeyless署名を実行させないためです。

## 4. SBOM

以下の2形式を生成します。

- CycloneDX JSON: `vaultsend.cdx.json`
- SPDX JSON: `vaultsend.spdx.json`

SPDX SBOMはGitHub Attestationにも関連付けます。

SBOMは依存関係の存在を示しますが、次の事項を保証しません。

- packageが安全であること
- ライセンス要件を満たしていること
- 実行時に読み込む外部componentをすべて含むこと
- 脆弱性が存在しないこと

調達・監査時はSBOM、脆弱性report、provenance、release承認記録を組み合わせます。

## 5. 脆弱性ゲート

Trivyを使用します。

### 自動拒否

次を満たす脆弱性が1件以上ある場合、Workflowを失敗させます。

- severityがHighまたはCritical
- upstreamから修正版が提供されている

### 要リスク判断

修正版が存在しないHigh / Critical脆弱性は、JSON reportへ残します。

自動的に安全扱いにはしません。リリース前に以下を判断します。

- 対象packageやコードパスを実際に使用しているか
- exploit条件を満たすか
- network・権限・設定による代替統制があるか
- base imageや依存packageを変更できるか
- 受容期限と再確認日

例外を認める場合は、IssueへCVE、影響、代替統制、承認者、期限を記録します。恒久的なignoreは避けます。

## 6. Secret・設定検査

リポジトリ全体を対象に、Trivyの以下のscannerを実行します。

- secret
- misconfiguration

High / Critical判定はPRを拒否します。

検査で検出されないSecretもあるため、次を継続します。

- Secret ManagerまたはDocker Secretを使用
- `.env`やcredential fileをGitへ追加しない
- 漏えい時は削除だけでなくcredentialを失効・再発行
- Git履歴とArtifactの残存も確認

## 7. 署名とAttestation

### Cosign keyless署名

GitHub ActionsのOIDC identityを利用します。長期秘密鍵をGitHub Secretsへ保存しません。

検証時は、次の両方を固定します。

- certificate identity: 対象repositoryの`/.github/workflows/supply-chain.yml`と実行ref
- OIDC issuer: `https://token.actions.githubusercontent.com`

署名が存在するだけでは不十分です。必ず期待するrepository、Workflow、refのidentityで検証します。

### GitHub Artifact Attestation

以下を登録します。

- build provenance
- SPDX SBOM

検証例:

```bash
gh auth login
docker login ghcr.io
gh attestation verify \
  oci://ghcr.io/mizzz-ivr/vaultsend@sha256:<digest> \
  --repo mizzz-ivr/vaultsend
```

Cosign検証例:

```bash
cosign verify \
  --certificate-identity \
  "https://github.com/mizzz-ivr/vaultsend/.github/workflows/supply-chain.yml@refs/heads/main" \
  --certificate-oidc-issuer \
  "https://token.actions.githubusercontent.com" \
  ghcr.io/mizzz-ivr/vaultsend@sha256:<digest>
```

release tagの場合はcertificate identityのrefを対象tagへ変更します。

## 8. GHCR運用

GitHub Actionsの`GITHUB_TOKEN`を使用してpushします。

初回package公開後に以下を確認します。

- repositoryとpackageが関連付いている
- package visibilityが意図した設定である
- 不要な外部team・userへread権限がない
- deployment環境だけにpull権限がある
- 古いtagを削除しても使用中digestを消さない

タグは変更可能な参照です。運用・デプロイ・ロールバック記録では必ずdigestを保存します。

## 9. 依存関係更新

`.github/dependabot.yml`で以下を週次確認します。

- GitHub Actions
- Docker base image
- Go modules
- npm packages

minor / patchはまとめ、major updateは個別判断します。

Dependabot PRも通常のCI、Operations、Supply Chain検査を通過させます。自動mergeは行いません。

## 10. ローカル確認

```bash
make verify-supply-chain
```

または:

```bash
bash scripts/verify-supply-chain.sh
```

生成物:

```text
artifacts/supply-chain/runtime-user.txt
artifacts/supply-chain/trivy-image-digest.txt
artifacts/supply-chain/source-security-report.txt
artifacts/supply-chain/vulnerabilities.json
artifacts/supply-chain/vulnerability-gate.txt
artifacts/supply-chain/vaultsend.cdx.json
artifacts/supply-chain/vaultsend.spdx.json
```

検査結果にはpackage名やfile pathが含まれる可能性があります。Artifactの閲覧権限と保持期間を管理します。

## 11. リリース前チェック

- 通常CIが成功している
- Operations Workflowが成功している
- Supply Chain Workflowが成功している
- High / Criticalの未修正脆弱性を確認した
- SBOM Artifactを取得できる
- GHCRの対象digestを記録した
- Cosign署名を期待identityで検証できる
- GitHub Attestationを検証できる
- package visibilityとpull権限を確認した
- デプロイ先がtagではなくdigestを使用している
- rollback先digestにも署名とAttestationがある

## 12. 障害時

### 脆弱性検出

1. 対象CVE、package、導入経路、修正版を確認
2. base imageまたは依存関係を更新
3. 新しいdigestをbuild・署名
4. 旧digestの稼働状況を確認
5. 必要に応じて緊急deploy
6. 対応記録と影響評価をIssueへ残す

### 署名・Attestation検証失敗

1. tagではなくdigestを確認
2. repository、Workflow、ref identityを確認
3. 対象Workflow runとcommit SHAを確認
4. packageをdeployしない
5. GitHub ActionsやOIDC設定の変更履歴を確認
6. 正規Workflowから再buildし、新しいdigestを発行

検証失敗したdigestへ署名だけを後付けして正規品扱いにはしません。ソースから再buildします。

### credential漏えい

1. 対象credentialを即時失効
2. package・Actions・監査ログを確認
3. 不正なimage、tag、workflow runの有無を確認
4. 正規digestを再検証
5. 影響範囲と対応をインシデント記録へ残す

## 13. 制約・後続課題

本対応では以下を完了条件に含めません。

- SLSAレベルの正式認証
- admission controllerによる未署名imageの強制拒否
- multi-architecture image
- SBOMの長期WORM保管
- ライセンスpolicyの自動ゲート
- Container registryのretention policy
- base image・scanner imageのdigest lock自動更新
- GitHub Actionsをすべてcommit SHAへ固定する移行
- release environmentの複数承認者設定

行政利用に向けた次段階では、デプロイ基盤側で署名・provenanceを必須化し、Workflowが生成した証跡を「確認できる」状態から「満たさないimageは実行できない」状態へ進めます。
