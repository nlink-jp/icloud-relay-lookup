# RFP: icloud-relay-lookup

> Generated: 2026-07-16
> Status: Draft

## 1. Problem Statement

アクセスログやアラートに現れる IP アドレスが **Apple iCloud Private Relay の出口 IP かどうか**を即座に判定するツールがない。Private Relay 出口からのアクセスは「匿名化経由だが、実体は Apple エコシステムの正規ユーザーである可能性が高い」という独特の文脈を持ち、Tor 出口や悪性プロキシとは扱いを分けるべきである。本ツールは Apple が公開する出口 IP レンジリストを用いて、単発またはバッチで IP を判定し、リストに含まれるジオヒント（国 / ISO 地域 / 都市）とともに返す。

対象ユーザーは、IR・ログ調査・アラートトリアージを行うセキュリティ担当者（および MCP 経由で同作業を行う AI エージェント）。asn-lookup（帰属）、abuse-lookup（評判）、tor-exit-lookup（Tor 出口）に続く IP コンテキスト判定ツール群の第4弾であり、tor-exit-lookup と対をなす「匿名化ネットワーク出口判定」の Apple 版である。

## 2. Functional Specification

### Commands / API Surface

CLI（単一バイナリ、MCP サーバー同居）:

| コマンド | 機能 |
|---------|------|
| `icloud-relay-lookup check <ip> [<ip>...]` | 単発/複数 IP の判定。引数なしなら stdin から IP リストを読む（1行1IP、ログから cut した列をそのまま流し込める） |
| `icloud-relay-lookup status` | キャッシュ状態表示（取得日時、ETag、行数、TTL 残） |
| `icloud-relay-lookup update` | リストの強制再取得（条件付き GET） |
| `icloud-relay-lookup mcp` | MCP サーバーとして起動（stdio） |

MCP ツール:

| ツール | 機能 |
|--------|------|
| `check_ip` | IP 判定（ジオヒント付き結果を返す） |
| `cache_status` | キャッシュ状態 |
| `update_list` | 強制再取得 |
| `get_usage` | ツールリファレンスとエラー回復表 |

### Input / Output

- 入力: IP アドレス（IPv4 / IPv6）。CLI は引数または stdin（1行1IP）
- 出力: JSON（`--json`）とヒューマンリーダブル表示の両対応

```json
{
  "ip": "172.224.226.34",
  "is_private_relay": true,
  "prefix": "172.224.226.34/31",
  "country": "GB",
  "region": "GB-EN",
  "city": "Oxford",
  "list_fetched_at": "2026-07-16T00:52:21Z"
}
```

- 非該当時は `is_private_relay: false` とし、ジオフィールドは省略
- バッチ時は JSONL で1行1結果

### Configuration

- 設定ファイル不要が基本（credential ゼロ）
- キャッシュディレクトリ: OS 標準のユーザーキャッシュ配下（`os.UserCacheDir()` ベース）。環境変数で上書き可
- TTL フロア: 1時間（Apple の `cache-control: max-age=3600` に整合）。期限内はネットワークアクセスなし、期限切れ後は ETag 条件付き GET で再検証（304 なら帯域ほぼゼロ）

### External Dependencies

- データソース: `https://mask-api.icloud.com/egress-ip-ranges.csv`（Apple 公開、認証不要）
  - 検証済み実測: 約28.7万行 / 12.1MB、IPv4 41,837 prefix + IPv6 245,093 prefix（ほぼ /64）
  - フォーマット: `prefix,国コード,ISO地域コード,都市,`（固定5フィールド、末尾空）
  - `ETag` + `cache-control: max-age=3600` 応答あり
- Go 外部ライブラリ依存: ゼロ（標準ライブラリのみ）

## 3. Design Decisions

- **言語: Go（外部依存ゼロ）** — asn-lookup / tor-exit-lookup と同じ。`net/netip` ベースの prefix 索引を asn-lookup から流用する。28.7万 prefix はメモリ上で問題なく扱える規模
- **単一バイナリに CLI + MCP 同居** — 組織の慣例（`mcp` サブコマンド方式）
- **新規 MCP 実装は data-toolbox-mcp の骨格を移植**（組織の標準パターン）。ただし本ツールは stateless 判定なので workspace モデルは不要
- **補完関係**: asn-lookup（IP→帰属）、abuse-lookup（IP→評判）、tor-exit-lookup（IP→Tor 出口）と並ぶ IP コンテキスト4点セットを完成させる。MCP を全部登録すれば、エージェントが1つの IP に対し帰属・評判・Tor・Private Relay の4文脈を横断取得できる
- **スコープ外（明記）**:
  - 逆引き列挙（国/地域 → prefix 一覧）— 需要が見えてから検討
  - Private Relay 以外の一般 VPN / プロキシ / 匿名化サービスの判定
  - リアルタイム監視・常駐動作

## 4. Development Plan

### Phase 1: Core

- リスト取得 + ETag 付きローカルキャッシュ（TTL フロア1時間）
- CSV 寛容パース（不正行はスキップしてカウント、フォーマット変化検知）
- `net/netip` prefix 索引の構築と判定ロジック
- `check` サブコマンド（単発 / 複数引数 / stdin バッチ、JSON / human 出力）
- ユニットテスト（HTTP はモック、実 CSV サンプルをテストデータ化）

### Phase 2: Features

- `status` / `update` サブコマンド
- MCP サーバー（`check_ip` / `cache_status` / `update_list` / `get_usage`）
- dummy MCP client harness による E2E テスト

### Phase 3: Release

- README.md / README.ja.md / CHANGELOG.md / AGENTS.md / LICENSE (MIT)
- `make build-all`（4プラットフォーム）、macOS は Developer ID 署名 + notarize
- GitHub リリース（zip 個別アップロード）、homebrew-tap 追加
- cybersecurity-series submodule 統合、org profile / web catalog 2面同期
- `check-org.sh` グリーン確認

各 Phase は独立レビュー可能。

## 5. Required API Scopes / Permissions

**None** — 公開 CSV のみを使用。API キー、OAuth、アカウント登録すべて不要。

## 6. Series Placement

Series: **cybersecurity-series**

Reason: tor-exit-lookup・abuse-lookup と同じ判定系セキュリティツール。匿名化ネットワーク出口判定という同一カテゴリの tor-exit-lookup と同居させるのが最も自然。

## 7. External Platform Constraints

- リストは約12MB。初回取得とフォーマット変更時のみフルダウンロード、以後は ETag 304 で帯域ほぼゼロ
- `cache-control: max-age=3600` — 1時間より高頻度の再取得は無意味
- **フォーマットは Apple の非公式仕様でバージョニングなし** — 列構成が予告なく変わり得る。寛容パース + 検証（パース成功率が閾値未満なら旧キャッシュ維持 + エラー報告）で防御する
- 認証・レート制限の公表なし。TTL フロア遵守で事実上問題にならない
- リストは「出口 IP レンジ」であり、個々の IP の現在のアクティブ状態までは表さない（Tor の exit-addresses に相当するメタソースはない）

---

## Discussion Log

- **データソース検証（2026-07-16）**: `mask-api.icloud.com/egress-ip-ranges.csv` を実測。286,930行 / 12.1MB、IPv4 41,837 + IPv6 245,093 prefix、235カ国、都市空欄2,140行、全行5フィールド固定。ETag + max-age=3600 を確認し、条件付き GET 戦略と TTL フロア1時間の根拠とした
- **ツール名**: `private-relay-lookup` / `icloud-relay-lookup` / `apple-relay-lookup` を比較し、Apple のサービス名に寄せた **icloud-relay-lookup** を採用
- **ユースケース**: IR/ログ調査 + アラートトリアージの2本柱。アクセス制御設計補助は主目的から外した
- **入力モード**: 単発 + stdin バッチを v0.1 から採用（ログ調査でバッチが効くため）
- **逆引き列挙**: スコープ外と決定（Phase 2 送りですらなく、需要が見えてから）。これにより workspace ファイル経由の大容量結果パターンが不要になり、stateless 設計を保てる
- **TTL**: tor-exit-lookup の30分フロアではなく、Apple の max-age=3600 に整合する1時間フロアを採用
- **ジオヒント**: Tor リストとの最大の違い。判定結果に国/地域/都市を同梱し、tor-exit-lookup より一段リッチな文脈を返す
