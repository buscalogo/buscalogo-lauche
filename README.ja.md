# BuscaLogo Agent（Launcher）

**言語:** [English](README.md) · [Português](README.pt.md) · [Español](README.es.md) · [日本語](README.ja.md)

[BuscaLogo](https://buscalogo.com) 分散型検索ネットワーク用のローカルデーモンです。1つの Go バイナリでメッシュ通信、`.bl` サイト用 DNS、スクレイピング、ストレージ、P2P 検索、Web 管理パネル、および任意のデスクトップ／ブラウザ連携を管理します。

## できること

| コンポーネント | 役割 |
|----------------|------|
| **Yggdrasil** | メッシュオーバーレイネットワーク（ピア + IPv6） |
| **CoreDNS** | `.bl` などのドメインをローカルで解決 |
| **CouchDB** | スクレイプ、ユーザー、設定を保存 |
| **Scraper** | Go ネイティブクローラ；ページを CouchDB に索引 |
| **P2P 検索** | シグナリング経由で他 Agent に問い合わせ |
| **アカウント** | ローカル登録／ログイン；ed25519 でスクレイプ署名 |
| **Web パネル** | `http://127.0.0.1:9970` の管理 UI |
| **デスクトップ** | Neutralino シェル + システムトレイ |
| **拡張機能** | Chrome/Firefox のステータス表示 + 索引提案 |

## 要件

- **Linux** amd64（主な対象；Ubuntu 22.04+ / Debian 12+ 向け `.deb`）
- **Go** 1.26+（ソースからビルドする場合）
- デスクトップビルド: Node.js + [`@neutralinojs/neu`](https://neutralino.js.org/)

## クイックスタート

### `.deb` からインストール

```bash
sudo dpkg -i buscalogo-agent_*_amd64.deb
buscalogo-agent
```

パネルを開く: [http://127.0.0.1:9970](http://127.0.0.1:9970)

### ソースからビルド

```bash
# 任意: Yggdrasil / CoreDNS / CouchDB バイナリを取得
make assets

make build          # → ./buscalogo-agent
make run            # ビルドして実行

# ポータブル tarball
make dist

# フルパッケージ（Agent + Neutralino デスクトップ + 拡張機能）
make deb
```

### デスクトップ（Neutralino）

```bash
npm install -g @neutralinojs/neu
make desktop-run      # 開発
make desktop-build    # 本番バンドル
```

詳細は [desktop/buscalogo-desktop/README.md](desktop/buscalogo-desktop/README.md)。

## 初回起動

1. Agent（またはデスクトップアプリ）を起動します。
2. アカウントがない場合 → **登録画面**。
3. アカウントはあるが未ログイン → **ログイン画面**（ログインするまでアプリはロック）。
4. セッションは `data/identity/session.json` に保存され、Agent 再起動後も維持されます。
5. プロフィールタブから **バックアップ JSON**（秘密鍵含む）をエクスポートし、安全な場所に保管してください。

スクレイプは ed25519 鍵で署名され、ネットワーク上で作者を特定できます。

## アーキテクチャ

```
                +-------------------------+
                |     BuscaLogo Agent     |
                |       (Go バイナリ)      |
                +-------------------------+
                   |      |        |
               CoreDNS  Yggdrasil  Scraper
                   |      |        |
                   +------+--------+
                          |
                 HTTP API  127.0.0.1:9970
                          |
              Web パネル / Neutralino ウィンドウ
                          |
                    ブラウザ拡張機能
```

データと同梱バイナリは Agent のホーム配下に置かれます（インストール時は通常 `/opt/buscalogo`、開発時は `data/`）。

## 設定

メインファイル: `config.yaml`（Agent データディレクトリ内）。

主なデフォルト:

| 項目 | デフォルト |
|------|------------|
| パネル / API | `127.0.0.1:9970` |
| CouchDB | `127.0.0.1:5984` |
| DNS | ローカル CoreDNS（`.bl` ドメイン） |
| Scraper | 有効 → CouchDB |

フラグ:

```bash
buscalogo-agent            # システムトレイ付き（利用可能な場合）
buscalogo-agent --no-tray  # ヘッドレス（Neutralino が使用）
```

## ブラウザ拡張機能

| フォルダ | 対象 |
|----------|------|
| `extension/chrome/` | Chrome, Chromium, Edge, Brave（MV3） |
| `extension/firefox/` | Firefox 109+（MV3） |

ストア掲載とパッケージ: [extension/README.md](extension/README.md) · [extension/store/STORE.md](extension/store/STORE.md)

Chrome Web Store: [BuscaLogo Agent](https://chromewebstore.google.com/detail/buscalogo-agent/gecmkbanhikgnhpcdibplcfndapclneh)

## プロジェクト構成

```
cmd/agent/           Agent エントリポイント
internal/            API、アカウント、スクレイパー、CouchDB、P2P、DNS、Yggdrasil、更新…
frontend/            埋め込み Web パネル（go:embed）
desktop/             Neutralino デスクトップシェル
extension/           ブラウザ拡張機能
assets/              同梱／取得バイナリ（Yggdrasil、CoreDNS、CouchDB）
sites/               .bl サイト設定の例
www/                 静的サイトアセット
dist/                パッケージ用スクリプトと成果物
```

## 開発

```bash
make build
make test
make vet
make fmt
```

リリースはタグ `v*` で [`.github/workflows/release.yml`](.github/workflows/release.yml) によりビルドされます。

## 関連リポジトリ

- **buscalogo.com** — 公開検索フロントエンド
- **bl-scraper-server** — スクレイパー／API 互換の参照実装
- **server** — BuscaLogo エコシステムのバックエンド

## 注意

BuscaLogo Agent は BuscaLogo プロジェクトの一部です。意図的にメッシュへ公開しない限り、API と CouchDB はローカルリスナーを推奨します。

---

**言語:** [English](README.md) · [Português](README.pt.md) · [Español](README.es.md) · [日本語](README.ja.md)
