# 日本語ドキュメント表記規約

- 状態: Baseline
- 更新日: 2026-08-09

## 1. 目的

日本語本文の可読性と差分の一貫性を保ちます。既存文書へ一括の機械変換を行って意味、code、identifier、link を壊すことは避け、変更行から段階的に適用します。

## 2. 基本規則

日本語本文と ASCII 英数字・技術用語が隣接する場合は、原則として境界に半角スペースを 1 つ入れます。

- `KIM では Host を管理する。`
- `VM の Placement を実行する。`
- `PostgreSQL authority から再構築する。`
- `Agent は Go で実装する。`

日本語の約物との境界にはスペースを要求しません。

- `KIM（KVM Infrastructure Manager）`
- `Host、VM、Network`
- `SR-IOV。`
- `「UNKNOWN」`

## 3. 対象外

次の内容は自動 spacing の対象外です。

- fenced code block、inline code、command、log、schema、table 内の code literal
- URL、email address、Markdown link destination
- Requirement、ADR、Invariant、Test、resource 等の識別子
- API path、package name、file path、environment variable
- Mermaid、JSON、YAML、SQL、Go 等の machine-readable content
- 製品名、規格名、引用文など、正規表記を保持すべき文字列

対象外であっても、周囲の日本語 prose との境界は安全に判定できる場合だけ手動で整えます。

## 4. Lint Policy

lint は次の順序で導入します。

1. `Advisory`: 新規・変更行だけを検査し、違反候補を warning として報告します。
2. `Scoped enforcement`: code/URL/identifier/約物を正しく除外できる rule だけを、変更行で blocking にします。
3. `Document migration`: 文書単位の人手 review を伴う formatting-only change set で既存行へ適用します。
4. `Repository enforcement`: false positive が解消し、全対象文書が migration 済みになった後だけ repository-wide blocking にします。

初期 lint rule は Unicode の日本語文字と ASCII letter/digit の直接隣接を候補として検出します。ただし自動修正は行わず、Markdown parser で code span、fence、link、identifier、約物を除外できるまで warning に留めます。

## 5. Change Rule

- 新しい文書は本規約へ従います。
- 既存文書を変更した場合は、意味を変えない範囲で変更した段落だけを整えます。
- spacing-only の repository-wide change を feature/architecture change と混在させません。
- lint suppression は対象文字列と理由を局所的に記録し、rule 全体を無効化しません。
- 表記修正によって code、ID、API、path、quote の内容を変更してはいけません。

## 6. Current Rollout Scope

Phase 0 formal exit では本規約の定義と、新規文書・今回変更した段落への安全な適用までを対象とします。既存 Architecture 全文の migration と blocking lint 実装は Developer Preview の documentation tooling task として追跡します。
