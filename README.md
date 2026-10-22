# Gopher Office（仮称）

Ebitengine（ブラウザ WASM）と Agones で作る、Gopher のバーチャルオフィス。

招待URLを開くと、同じオフィスに Gopher として入れる。歩き回る姿が全員に同期され、数百体の Gopher で画面を埋め尽くせる。

> **ステータス:** P1 完了（ローカルで 1 体の Gopher が歩ける）。オンライン化はこれから。
> 最初のマイルストーンは 2026-11-06 の [Ebitengine ぷちConf #5](https://gocon.connpass.com/event/405972/) の LT デモ。

## 特徴（v0 の予定）

- 招待URLでの入室（`/w/{inviteCode}`）
- Gopher のアバター。最初に性別を選ぶ（男性＝青、女性＝赤、その他＝黄）
- WASD またはクリックで移動（壁を避けて歩く）
- 表示名とプロフィールアイコン画像のカスタマイズ
- 数百人規模の同時表示（入室人数の上限あり）
- 音声・動画通話は LT 後（P2P メッシュで検証予定）

## 構成

ゲームサーバ API とリアルタイムサーバに分けた、オンラインゲームの構成を採る。

```
Browser (Ebitengine WASM)
   │ HTTPS / WSS
Cloudflare Tunnel → ingress-nginx
   ├─ /v1/*  → office-api      （認証・招待・プロフィール・ルーム割り当て）
   ├─ /room  → office-gateway  （WebSocket 中継）→ GameServer（Agones、ルーム 1 つにつき 1 つ）
   └─ /      → office-web      （静的配信）
```

実行基盤はおうち k3s（ArgoCD）。詳細は [docs/design.md](docs/design.md)。

## クイックスタート

```bash
make serve   # ブラウザ版（WASM）をオフラインでビルド・配信 → http://localhost:8080
make dev     # ブラウザ版と GameServer（-local）を起動 → http://localhost:7654/?online=1
make bot N=100   # 別のターミナルで。bot を 100 体つなぐ（Gopher で画面が埋まる）
make run     # デスクトップ版を起動（オフライン）
make test    # テスト
```

操作: 起動後に Gopher（性別）を選ぶ。`WASD` で移動、マップをクリック（タップ）するとその地点へ歩く。

### 招待 URL で入る（API + MySQL + GameServer）

```bash
make db           # MySQL を Docker で起動（初回は数十秒待つ）
make migrate      # テーブルを作る
make api          # ターミナル 1: API を起動
make dev-invite   # ターミナル 2: GameServer（roomToken を検証）とブラウザ版を配信
make invite       # ワークスペースを作って招待 URL を表示する
```

表示された URL の末尾に `?api=http://localhost:8080` を付けて開く（ブラウザ版と API のオリジンが違うため）。
同じ URL を複数のタブで開けば、同じルームで会える。`go run ./cmd/bot -api http://localhost:8080 -invite <コード> -n 100` で bot も入れる。
秘密鍵は `make dev-env` が `.env.dev` にランダムに作る（コミットされない）。DB を使うテストは `make test-all`。

### ブラウザ版のクエリ

ブラウザ版は URL で接続先を決める。何も付けなければオフライン（自分だけ）で動く。

| クエリ | 意味 |
|---|---|
| `/w/{招待コード}` | 招待 URL。API でゲスト登録・入室して、返された GameServer につなぐ |
| `?api=http://host:8080` | API のベース URL（既定は同じオリジン） |
| `?online=1` | API を通さず、同じオリジンの `/room` に直接つなぐ（開発用） |
| `?server=ws://host:7654/room` | 接続先を指定する |
| `?name=たいち` | 表示名 |

デスクトップ版は `go run ./cmd/client -server ws://localhost:7654/room -name たいち`。

## 開発ルール

- ブラウザ（WASM）を主要ターゲットとする。変更後は `GOOS=js GOARCH=wasm go build ./cmd/client` が通ること
- クライアントのコードで `os/exec`、ファイルI/O、生の TCP/UDP ソケットを使わない
- ロードマップは本ファイルで管理する。項目を完了したら、同じ変更の中で `⬜` → `✅` に更新する

## ロードマップ

| Phase | 内容 | 状態 |
|---|---|---|
| P0 | 設計（[docs/design.md](docs/design.md)） | ✅ |
| P1 | `internal/sim`（マップ・衝突・経路探索・移動）、Gopher 3 色、背景、クレジット表示 | ✅ |
| P2a | proto 定義、`cmd/gameserver`（`-local`）、クライアントのオンライン接続、`cmd/bot` | ✅ |
| P2b | API（ゲスト認証、ワークスペース、招待URL入室、roomToken の検証）、マイグレーション | ✅ |
| P2c | アイコン画像のアップロード・配信、プロフィール編集の UI | ⬜ |
| P3 | Agones 導入、Fleet、`cmd/gateway`、Tunnel 公開 | ⬜ |
| P4 | 200 体の負荷試験（Sync の帯域削減を含む）、事前録画、LT リハーサル | ⬜ |
| P5 | P2P 音声通話、近接音声、動画 | ⬜ |
| P6 | 複数ルーム・ワークスペース UI、永続アカウント | ⬜ |

## クレジット

**The Go gopher was designed by [Renée French](https://reneefrench.blogspot.com/).**
Licensed under [CC BY 3.0](https://creativecommons.org/licenses/by/3.0/). 本プロジェクトでは色を変える改変を加えている（青・赤・黄）。

アプリ内では、このクレジットを画面中央に常時表示する。
