# icloud-relay-lookup

その IP アドレスは **Apple iCloud Private Relay の出口 IP** か？
`icloud-relay-lookup` は Apple が公開する
[`egress-ip-ranges.csv`](https://mask-api.icloud.com/egress-ip-ranges.csv)
（約29万レンジ）のローカルキャッシュからオフラインで即答します。`update` で
一度リストを取得すれば（自動再検証も可）、以後の `check` はメモリ上の
longest-prefix match — ネットワーク不要、認証情報不要。ヒット時には Apple が
リストに載せているジオヒント（国 / ISO 地域 / 都市）も返します。

Private Relay 出口からのアクセスは「匿名化経由だが、実体は Apple エコシステムの
正規ユーザーである可能性が高い」という独特の文脈を持ち、Tor 出口やオープン
プロキシとは扱いを分けるべきものです。
[`tor-exit-lookup`](https://github.com/nlink-jp/tor-exit-lookup)（Tor 出口）、
[`asn-lookup`](https://github.com/nlink-jp/asn-lookup)（AS / 国）、
[`abuse-lookup`](https://github.com/nlink-jp/abuse-lookup)（評判）の姉妹品で、
4つの角度から IP をプロファイルできます（CLI パイプでも MCP でも）。

## インストール

Homebrew（macOS, Apple Silicon — 署名 + notarize 済みビルド済みバイナリ）:

```sh
brew install nlink-jp/tap/icloud-relay-lookup
```

または [リリースページ](https://github.com/nlink-jp/icloud-relay-lookup/releases)
から linux/amd64、linux/arm64、darwin/arm64、windows/amd64 のビルド済み
バイナリを取得してください。

ソースからビルド（Go 1.25+）:

```sh
git clone https://github.com/nlink-jp/icloud-relay-lookup
cd icloud-relay-lookup
make build          # → dist/icloud-relay-lookup
```

## クイックスタート

```sh
# 1. Apple の出口リストを取得（公開エンドポイント・認証不要・約12MB）:
icloud-relay-lookup update

# 2. アドレスを判定:
icloud-relay-lookup check 172.224.226.34
# → 172.224.226.34 is an iCloud Private Relay egress IP  [Oxford, GB-EN, GB — 172.224.226.34/31]

icloud-relay-lookup check 8.8.8.8
# → 8.8.8.8 is not an iCloud Private Relay egress IP        （終了コード 1）

# 3. スクリプトで終了コードを利用:
if icloud-relay-lookup check "$ip"; then
  echo "$ip は Private Relay 経由"
fi

# 4. ログの IP を一括判定:
cut -f1 access.log | icloud-relay-lookup check --json | jq 'select(.is_private_relay)'
```

## コマンド

| コマンド | 説明 |
|---------|------|
| `check <IP>...` | 各 IP が Private Relay 出口かを判定（引数なしなら stdin を読む） |
| `update` | 出口リストを再検証/ダウンロードしローカルストアを再構築 |
| `status` | キャッシュ済みリストの鮮度・件数・ETag を表示 |
| `mcp` | ローカル MCP サーバーとして起動（stdio） |
| `version` | バージョンを表示 |

### `check` のモードと終了コード

テキストモードで単一 IP を渡した場合は grep 流の規約で、シェルに組み込めます:

| コード | 意味 |
|--------|------|
| `0` | その IP は Private Relay 出口 IP **である** |
| `1` | その IP は Private Relay 出口 IP **ではない** |
| `2` | エラー（不正な IP、ローカルリストなし、…） |

それ以外の形 — 複数 IP、stdin 入力、`--json` — は**バッチモード**: 結果は
stdout に 1 IP 1 行、終了コードはエラー有無のみ（`0` / `2`）。`--json` は
JSON Lines を出力します
（`{ip, is_private_relay, prefix?, country?, region?, city?, checked_at, list_fetched_at}`）。

## 自動更新

Apple はリストを `ETag` + `cache-control: max-age=3600` 付きで配信しています。
デフォルトでは、キャッシュが TTL（デフォルト1時間、Apple の max-age に合わせ
1時間がフロア）より古いとき `check` が条件付き GET で再検証します — リストが
変わっていなければ `304 Not Modified` で帯域はほぼゼロです。再検証に失敗した
場合（オフライン等）は、エラーにせず警告付きでキャッシュを使い続けます。
`--no-update`（per-call）または `[apple] auto_update = false`（全体）で無効化
できます。

上流の CSV フォーマットは Apple の非公式仕様でバージョニングがないため、行が
パースできなくなったダウンロードは拒否し、直前のキャッシュを維持します。

## MCP サーバー

`icloud-relay-lookup mcp` は stdio 上の JSON-RPC 2.0（標準ライブラリのみ）。
ツールは `check_ip`、`cache_status`、`update_list`、`get_usage`（組み込みの
操作マニュアル。initialize の `instructions` フィールドでも案内されます）。
登録例:

```json
{
  "mcpServers": {
    "icloud-relay-lookup": { "command": "icloud-relay-lookup", "args": ["mcp"] }
  }
}
```

## 設定

認証情報は不要です — エンドポイントは公開されています。すべてに妥当な
デフォルトがあり、設定ファイル・環境変数・フラグで上書きできます。

```toml
# ~/.config/icloud-relay-lookup/config.toml
[apple]
# url = "https://mask-api.icloud.com/egress-ip-ranges.csv"
# ttl_minutes = 60      # 自動再検証のしきい値（フロア 60）
# auto_update = true    # 古いとき check で自動再検証

[store]
# dir = "~/.local/share/icloud-relay-lookup"
```

| 設定 | 環境変数 | フラグ | デフォルト |
|------|---------|--------|-----------|
| リスト URL | `ICLOUD_RELAY_LOOKUP_URL` | `--url`（update, mcp） | `…/egress-ip-ranges.csv` |
| ストアディレクトリ | `ICLOUD_RELAY_LOOKUP_STORE_DIR` | `--store-dir` | `~/.local/share/icloud-relay-lookup` |
| TTL（分） | `ICLOUD_RELAY_LOOKUP_TTL_MINUTES` | — | `60`（最小 60） |
| 自動更新 | `ICLOUD_RELAY_LOOKUP_AUTO_UPDATE` | `--no-update`（無効化） | `true` |
| 設定ファイル | — | `-c`, `--config` | `~/.config/icloud-relay-lookup/config.toml` |

## 仕組み

`update` は出口リスト全体（IPv4 + IPv6 約29万 CIDR レンジ）を取得し、CSV を
そのまま、取得時刻・ETag・件数を持つ小さな `meta.json` と並べて保存します
（temp + rename のアトミック書き込み）。`check` はキャッシュ済み CSV を
prefix 長ごとのハッシュマップ（実リストは約19種）にパースするため、1回の
ルックアップは最大でも十数回のマップ参照で、長い prefix から順に照合します。
アドレスは正規化（`Unmap`）されるので v4-in-v6 入力もマッチします。鮮度は
ファイルの mtime ではなく `meta.json` 内に記録されるためコピーしても保たれ、
ローカルコピーが7日を超えると `status` が警告します。

シグナルの向きに注意してください: このリストが示すのは Apple の出口レンジと
その提供対象地域です。ヒットは「その接続がその地域近辺の Private Relay を
経由してきた」ことを教えてくれますが、ユーザー個人を特定するものではありません
— それこそが Private Relay の目的です。

## 開発

```sh
make test        # go test -race -cover ./...
make check       # lint + test + build-all
```

外部依存なし — 標準ライブラリのみ。設計の背景は
[docs/ja/architecture.ja.md](docs/ja/architecture.ja.md) を参照してください。

## ライセンス

MIT — [LICENSE](LICENSE) を参照。出口リストのデータは
[Apple](https://developer.apple.com/support/prepare-your-network-for-icloud-private-relay/)
が公開しているものです。キャッシュはローカルに保持され、再配布はしません。
