# Gopher Office（仮称）

Ebitengine（ブラウザ WASM）と Agones で作る、Gopher のバーチャルオフィス。

招待URLを開くと、同じオフィスに Gopher として入れる。歩き回る姿が全員に同期され、数百体の Gopher で画面を埋め尽くせる。

> **ステータス:** 設計中（Draft）。実装はこれから。
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

## 開発ルール

- ブラウザ（WASM）を主要ターゲットとする。変更後は `GOOS=js GOARCH=wasm go build ./cmd/client` が通ること
- クライアントのコードで `os/exec`、ファイルI/O、生の TCP/UDP ソケットを使わない
- ロードマップは本ファイルで管理する。項目を完了したら、同じ変更の中で `⬜` → `✅` に更新する

## ロードマップ

| Phase | 内容 | 状態 |
|---|---|---|
| P0 | 設計（[docs/design.md](docs/design.md)） | ✅ |
| P1 | `internal/sim`（マップ・衝突・経路探索・移動）、Gopher 3 色、背景、クレジット表示 | ⬜ |
| P2 | proto 定義、`cmd/gameserver`、オンライン接続、`cmd/bot`、招待URL入室 | ⬜ |
| P3 | Agones 導入、Fleet、`cmd/gateway`、Tunnel 公開 | ⬜ |
| P4 | 200 体の負荷試験、事前録画、LT リハーサル | ⬜ |
| P5 | P2P 音声通話、近接音声、動画 | ⬜ |
| P6 | 複数ルーム・ワークスペース UI、永続アカウント | ⬜ |

## クレジット

**The Go gopher was designed by [Renée French](https://reneefrench.blogspot.com/).**
Licensed under [CC BY 3.0](https://creativecommons.org/licenses/by/3.0/). 本プロジェクトでは色を変える改変を加えている（青・赤・黄）。

アプリ内では、このクレジットを画面中央に常時表示する。
