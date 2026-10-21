# Gopher Office（仮称）— バーチャルオフィス 設計書

**ステータス:** Draft
**作成日:** 2026-10-21
**関連:** 参考設計 `~/src/ebiten/docs/online-design.md`（Wave Duel オンライン化）、インフラ: `o-ga09/infra`（おうちk3s / ArgoCD）
**最初のマイルストーン:** 2026-11-06 [Ebitengine ぷちConf #5](https://gocon.connpass.com/event/405972/) の LT でデモ

---

## 目次

1. [目的とスコープ](#1-目的とスコープ)
2. [決定事項サマリ](#2-決定事項サマリ)
3. [機能要件（v0）](#3-機能要件v0)
4. [全体アーキテクチャ](#4-全体アーキテクチャ)
5. [ルームと GameServer のライフサイクル](#5-ルームと-gameserver-のライフサイクル)
6. [ネットコード](#6-ネットコード)
7. [ゲーム API サーバ](#7-ゲーム-api-サーバ)
8. [プロフィールとアイコン画像](#8-プロフィールとアイコン画像)
9. [クライアント（Ebitengine WASM）](#9-クライアントebitengine-wasm)
10. [インフラ（おうちk3s）](#10-インフラおうちk3s)
11. [音声・動画通話（LT 後）](#11-音声動画通話lt-後)
12. [リポジトリ構成](#12-リポジトリ構成)
13. [開発フェーズとスケジュール](#13-開発フェーズとスケジュール)
14. [リスク・未決事項](#14-リスク未決事項)

---

## 1. 目的とスコープ

ovice のような 2D バーチャルオフィスを、**Ebitengine（ブラウザ WASM）＋ Agones** で作る。Wave Duel のオンライン化設計と同じく、**ゲーム API サーバ**（永続データ・認証・ルーム割り当て）と**リアルタイムサーバ**（Agones の GameServer）に分ける。

### 当面のゴール

2026-11-06 の LT で、観客がURLを開くだけで同じオフィスに入り、**Gopher が画面を埋め尽くす**デモを動かす。

### v0 のスコープ内

- 招待URLでの入室
- アバター（Gopher）の移動と全員への同期（WASD / クリック移動）
- アバターのカスタマイズ（名前、プロフィールアイコン画像、性別による色）
- 数百体規模の同時表示

### スコープ外（LT 後）

- 音声通話、動画通話（[§11](#11-音声動画通話lt-後)）
- レイアウト編集、複数フロア、複数ワークスペースの切り替え UI
- 永続アカウント（ソーシャルログイン等）
- チャット、近接チャットバブル
- モバイル向けタッチ操作の最適化

---

## 2. 決定事項サマリ

| # | 論点 | 決定 | 主な理由 |
|---|---|---|---|
| D1 | クライアント | **Ebitengine の WASM ビルド** | インストール不要で、招待URLだけで入れる |
| D2 | トランスポート | **WebSocket（TCP）＋ Protocol Buffers** | Cloudflare Tunnel を通せる唯一の実用手段（Wave Duel D3 と同じ） |
| D3 | 外部公開 | Tunnel → ingress-nginx → gateway → GameServer Pod | 自宅IPを公開しない（Wave Duel D4 と同じ） |
| D4 | GameServer の粒度 | **ルーム 1 つ = GameServer 1 つ** | ルームが無人になれば Shutdown でき、スケールの単位が明確 |
| D5 | 権威モデル | サーバ権威（位置・衝突）。移動は「意図」を同期し各クライアントが補間 | 数百人でも帯域を抑えられる（[§6](#6-ネットコード)） |
| D6 | マップ | **固定 1 枚**、オフィス風背景、タイル衝突判定 | v0 の工数を絞る |
| D7 | アバター | **Gopher 固定**。性別で色が決まる（男性＝青、女性＝赤、その他＝黄） | 要望 |
| D8 | 認証 | ゲストログイン（表示名・性別・アイコン） | 永続アカウントは LT 後 |
| D9 | 招待 | `/w/{inviteCode}` の URL がワークスペースを指す。v0 は 1 ワークスペース＝1 ルーム | 複数ルームは構造だけ用意し、UI は後回し |
| D10 | クレジット | **CC BY 3.0 の Gopher クレジットを画面中央に常時表示（Must）** | [§9.3](#93-gopher-クレジット表記must) |
| D11 | 通話（LT 後） | 検証用に **P2P メッシュ（WebRTC 直結）＋自前シグナリング** | まず最小構成で検証。人数上限は割り切る |
| D12 | DB / Redis | 既存クラスタの MySQL / Redis | 新規ミドルウェアを増やさない |
| D14 | API サーバの技術選定 | Web フレームワーク **Echo v5**（`github.com/labstack/echo/v5`）、ORM **GORM**（`gorm.io/gorm` + `gorm.io/driver/mysql`）、バリデーション **go-playground/validator v10** | ユーザー指定 |
| D13 | ドメイン | `*.o-ga09.com`（仮: `office.o-ga09.com`） | 既存 Tunnel 設定を流用 |

---

## 3. 機能要件（v0）

### 入室フロー

1. 招待URL `https://office.o-ga09.com/w/{inviteCode}` を開く
2. 初回のみ**プロフィール設定**: 表示名、性別（男性・女性・その他）、アイコン画像（任意）
3. 入室。マップ上のスポーン位置に Gopher が現れ、既にいる全員が見える

### 移動

- **WASD**: 押している間、その方向へ移動
- **クリック移動**: クリックした地点へ、壁を避けて歩く（経路探索は単純なグリッド A*）
- 壁や家具（衝突タイル）には入れない

### 表示

- 全員を同じ画面に表示する（視界の制限なし）
- Gopher の頭上に**表示名**、横または名前タグ内に**プロフィールアイコン**
- 画面中央に **Gopher クレジット**（常時）

### 性別と色

| 選択 | Gopher の色 |
|---|---|
| 男性 | 青 |
| 女性 | 赤 |
| その他 | 黄 |

### 入室人数の上限

- ルームごとに上限を持つ（既定 200、設定で変更可）。超えたら入室を拒否し「満員」を返す
- 表示名の**文字種は制限しない**。ただし技術的な制約として**長さは最大 32 文字（rune 数）**、制御文字・改行は除去する（描画崩れとログ汚染、帯域を守るため）

---

## 4. 全体アーキテクチャ

```mermaid
flowchart LR
  C[Ebiten Client<br/>Browser WASM]
  CF[Cloudflare Tunnel]
  IN[ingress-nginx]
  WEB[office-web<br/>静的配信]
  API[office-api<br/>Deployment]
  GW[office-gateway<br/>Deployment]
  subgraph Agones Fleet
    GS1[GameServer Pod<br/>room A]
    GS2[GameServer Pod<br/>room B]
  end
  MY[(MySQL)]
  RD[(Redis)]

  C -- HTTPS --> CF --> IN
  C -- WSS --> CF
  IN -- / --> WEB
  IN -- /v1/* --> API
  IN -- /room --> GW
  GW -- WS (PodIP:7654) --> GS1
  API -- GameServerAllocation --> GS1
  API --- MY
  API --- RD
```

Wave Duel との対応:

| Wave Duel | 本プロジェクト | 変更点 |
|---|---|---|
| wave-api | office-api | マッチングが不要になり、ワークスペース・招待・プロフィール・アイコンが加わる |
| wave-gateway | office-gateway | ほぼそのまま。トークンの中身がルーム向けになる |
| wave-gameserver（試合） | office-gameserver（ルーム） | 60Hz の対戦判定ではなく、20Hz の位置同期。短命な試合から長命なルームに変わる |
| `internal/sim` | `internal/sim` | 波の代わりに、マップ・衝突・経路探索・移動。クライアントとサーバで共有 |

### 公開エンドポイント（単一ホスト・パス分け）

| ホスト | パス | 転送先 |
|---|---|---|
| `office.o-ga09.com` | `/` | office-web（`index.html` / `game.wasm` / `wasm_exec.js`） |
| 同上 | `/v1/*` | office-api |
| 同上 | `/room` | office-gateway（WebSocket） |
| `office.home.local` | 同上 | LAN 内確認用 |

---

## 5. ルームと GameServer のライフサイクル

試合型（Wave Duel）との最大の違い。**ルームは長命で、人がいる間だけ GameServer が存在する。** ワークスペースやプロフィールなど永続データは API と DB が持つ。ルーム内の位置は揮発する。

```mermaid
sequenceDiagram
  participant C as Client
  participant A as office-api
  participant R as Redis
  participant K as Agones (k8s API)
  participant G as office-gateway
  participant S as GameServer

  C->>A: POST /v1/guest（表示名・性別）
  A-->>C: playerToken
  C->>A: POST /v1/workspaces/{code}/join
  A->>R: SETNX room:{id}:lock
  alt GameServer 未割り当て
    A->>K: create GameServerAllocation（annotation: roomId）
    K-->>A: gameServerName
    A->>R: room:{id} → gsName（TTL 付き、GS が更新）
  else 割り当て済み
    A->>R: GET room:{id}
  end
  A-->>C: roomToken（roomId, playerId, gsName）
  C->>G: WSS /room?token=...
  G->>G: トークン検証 → gsName から PodIP 解決
  G->>S: WS 接続（中継）
  C->>S: Hello{roomToken, profile}
  alt 満員
    S-->>C: Error{Full}
  else
    S-->>C: Welcome{自分のid, 全員の状態}
    S-->>C: （他の参加者へ）PlayerJoined
  end
```

### GameServer の状態遷移

| 状態 | きっかけ |
|---|---|
| Ready | Fleet が事前に待機させている |
| Allocated | 最初の入室要求で API が割り当てる。annotation の `roomId` を読む |
| 稼働中 | 入室・退室を処理し、20Hz で位置をシミュレーション |
| Shutdown | **無人が 5 分続いたら** `SDK.Shutdown()`（Fleet が代わりを補充） |

- 同時に複数人が入室しても割り当てが重複しないよう、API は Redis のロック（`SETNX`、短い TTL）で直列化する
- GameServer は自分の `roomId` と、`Hello` のトークン内 `roomId` が一致しなければ切断する
- 満員判定は GameServer が権威。`Hello` を受けた時点で人数を数え、上限なら `Error{Full}` を返す
- GameServer が落ちた場合、API の `join` は Redis の割り当てが無効（GameServer が存在しない）なら再割り当てする。位置は揮発するので、全員がスポーン位置から入り直す

---

## 6. ネットコード

### 帯域の見積もり（なぜ「意図」を同期するか）

200 人が同じルームにいる場合を考える。

- **毎 tick 全員の座標を全員に送る場合**: 1 人 6 バイト × 200 人 × 20Hz ≈ 24 KB/s を 200 クライアントへ ≈ **4.8 MB/s（約 38 Mbps）**。自宅回線のアップロードでは厳しい
- **移動の意図を送る場合**（本設計）: 位置が変わる**イベントだけ**を送る。各クライアントは受け取った意図から補間して歩かせる
  - 200 体が 5 秒に 1 回行き先を変えるなら、40 イベント/s × 約 16 バイト × 200 宛 ≈ **128 KB/s（約 1 Mbps）**
  - ドリフト補正のため全員の座標を **1Hz で配る**場合でも、1.2 KB × 200 宛 ≈ 240 KB/s（約 2 Mbps）

### 方針

- サーバ tick は **20Hz**（`internal/sim` を共有）
- 時間は整数 tick で進める。`time.Now()` や描画フレームに依存しない
- 移動は**決定的**に計算できる状態で送る: `{startPos, target(or direction), speed, startTick}`
- クライアントは自分の移動を**先行して表示**（予測）し、サーバの補正に滑らかに合わせる。他人は受け取った意図から補間して表示する

### メッセージ（Protocol Buffers、WebSocket バイナリフレーム）

| 方向 | メッセージ | 頻度 | 内容 |
|---|---|---|---|
| C→S | `Hello` | 接続時 | roomToken、クライアントバージョン、プロフィール（表示名・性別・アイコンID） |
| C→S | `MoveTo` | クリック時 | 目的地（タイル座標） |
| C→S | `MoveDir` | 入力が変わった時 | 方向（8方向＋停止）。押しっぱなしで毎フレーム送らない |
| C→S | `Ping` | 1Hz | クライアント時刻（RTT 計測、Cloudflare の 100 秒アイドル対策） |
| S→C | `Welcome` | 接続時 | 自分の `playerId`、マップID、サーバ tick、全員の現在状態 |
| S→C | `PlayerJoined` / `PlayerLeft` | 即時 | プロフィール、スポーン位置 |
| S→C | `Moved` | 即時（変化時） | `playerId`, 現在位置, 目的地または方向, 速度, 基準 tick |
| S→C | `Sync` | 1Hz | 全員の現在位置（量子化）。ドリフト補正用 |
| S→C | `Pong` | 1Hz | エコー＋サーバ tick |
| S→C | `Error` | 即時 | `Full`, `BadToken` など |

- 座標は 1 タイル未満の精度を保つよう量子化（例: 1/16 タイル単位の整数）。1 人 4〜6 バイト
- `Moved` はブロードキャスト時に同一 tick のものをまとめて 1 フレームで送る
- WebSocket ライブラリは WASM でも動く `github.com/coder/websocket`

### サーバ側の最適化

- ブロードキャストは各接続ごとの送信キュー（バッファ付き goroutine）。遅いクライアントのキューが溢れたら**古い `Sync` を捨てる**（`Moved` は捨てない）
- シミュレーションと送信は分離し、1 ルーム 1 goroutine がシミュレーションを持つ（ロック競合を避ける）

---

## 7. ゲーム API サーバ

### 技術スタック

| 用途 | 採用 | 備考 |
|---|---|---|
| Web フレームワーク | Echo v5（`github.com/labstack/echo/v5`） | v4 とは import パスと API が異なる（ハンドラの引数など）。実装時に v5 のドキュメントで確認する |
| ORM | GORM（`gorm.io/gorm`、`gorm.io/driver/mysql`） | 下記のデータモデルは GORM のモデルとして定義する |
| バリデーション | go-playground/validator v10 | リクエスト DTO の struct タグで検証する |

- **バリデーションの適用範囲**: 表示名は 1〜32 文字（rune 数。`validate:"required,min=1,max=32"` は文字数で数えられる）、性別は 0〜2、招待コードは英数字 12 文字程度。制御文字の除去は validator ではなく入力の正規化で行う
- **マイグレーション**: v0 は GORM の `AutoMigrate` を使わず、`migration-job.yaml`（コンテナ名 `migration`）で SQL を適用する方針とする。AutoMigrate は本番の起動時にスキーマを書き換えるため、Fleet や複数レプリカと相性が悪い
- **画像アップロード**: `PUT /v1/me/icon` は Echo のボディサイズ制限（256 KB）をミドルウェアで掛ける
- **認証**: playerToken は Echo のミドルウェアで検証し、`playerId` をコンテキストに載せる。`/internal/*` は別ミドルウェアで internalToken を検証する

### エンドポイント

| メソッド | パス | 説明 |
|---|---|---|
| `POST` | `/v1/guest` | ゲスト作成（表示名・性別）。`playerToken`（HS256、30 日）を返す |
| `PATCH` | `/v1/me` | 表示名・性別を変更 |
| `GET` | `/v1/me` | 自分のプロフィール |
| `PUT` | `/v1/me/icon` | アイコン画像のアップロード（[§8](#8-プロフィールとアイコン画像)） |
| `GET` | `/v1/players/{id}/icon` | アイコン画像の取得（キャッシュ可） |
| `POST` | `/v1/workspaces` | ワークスペース作成。`inviteCode` を返す |
| `GET` | `/v1/workspaces/by-code/{code}` | 招待コードからワークスペース情報を取得 |
| `POST` | `/v1/workspaces/{code}/join` | 入室。`roomToken` を返す（必要なら GameServer を割り当てる） |
| `POST` | `/internal/rooms/{id}/closed` | GameServer からの閉鎖通知（内部トークン認証、Ingress には出さない） |
| `GET` | `/healthz` | ヘルスチェック |

### データモデル（MySQL）

以下の DDL が目標とするスキーマ。GORM のモデル（`internal/api/model`）はこれに合わせて定義する。

```sql
CREATE TABLE players (
  id            CHAR(26)     PRIMARY KEY,        -- ULID
  display_name  VARCHAR(128) NOT NULL,           -- 32 rune 以内。バイト余裕を持たせる
  gender        TINYINT      NOT NULL,           -- 0=男性(青) 1=女性(赤) 2=その他(黄)
  icon_hash     CHAR(64)     NULL,               -- アイコン画像の SHA-256
  created_at    DATETIME(3)  NOT NULL
);

CREATE TABLE player_icons (
  hash       CHAR(64)    PRIMARY KEY,            -- 再エンコード後の画像の SHA-256
  data       MEDIUMBLOB  NOT NULL,               -- 64x64 PNG
  created_at DATETIME(3) NOT NULL
);

CREATE TABLE workspaces (
  id           CHAR(26)    PRIMARY KEY,
  name         VARCHAR(64) NOT NULL,
  invite_code  VARCHAR(16) NOT NULL UNIQUE,      -- 推測困難なランダム文字列
  max_players  INT         NOT NULL DEFAULT 200,
  created_at   DATETIME(3) NOT NULL
);

CREATE TABLE rooms (
  id            CHAR(26)    PRIMARY KEY,
  workspace_id  CHAR(26)    NOT NULL,
  name          VARCHAR(64) NOT NULL,
  map_id        VARCHAR(32) NOT NULL DEFAULT 'office-1'
);
```

v0 では「ワークスペース作成時にルームを 1 つ自動作成」する。

### Redis

| キー | 用途 |
|---|---|
| `room:{id}:lock` | GameServer 割り当ての直列化（短 TTL） |
| `room:{id}` | `gsName`。GameServer が定期的に TTL を延長する（生存確認を兼ねる） |

### トークン

| 種類 | 用途 | 中身 | 有効期限 |
|---|---|---|---|
| playerToken | API 認証 | `sub=playerId` | 30 日 |
| roomToken | ゲートウェイ / GameServer への入室 | `roomId, playerId, gsName` | 60 秒 |
| internalToken | GameServer → API | 固定の共有シークレット | — |

招待コードは 12 文字程度の base62 乱数（約 71 ビット）とし、推測不能にする。署名鍵は SealedSecret で配布する。

---

## 8. プロフィールとアイコン画像

アップロード画像は**プロフィールアイコン専用**。Gopher の体には貼らない（体は常に Gopher）。

### アップロード処理（`PUT /v1/me/icon`）

1. リクエストサイズを **256 KB 以下**に制限する
2. `Content-Type` ではなく**中身から画像形式を判定**する（PNG / JPEG / WebP のみ許可）
3. デコードし、寸法上限（例: 4096×4096）を超えるものは拒否する（decompression bomb 対策）
4. **64×64 に縮小して PNG に再エンコード**する（EXIF などのメタデータが除去される）
5. SHA-256 を計算し、`player_icons` に保存する（同一内容は重複保存しない）。`players.icon_hash` を更新する

### 配信

- `GET /v1/players/{id}/icon` は `ETag` に `icon_hash` を使い、`Cache-Control` を付ける。Cloudflare 側でキャッシュできる
- GameServer の `Welcome` / `PlayerJoined` には**画像そのものではなく `icon_hash` だけ**を載せる。クライアントが必要に応じて取得し、メモリにキャッシュする
- 数百人が一斉に入るとアイコン取得が集中するため、取得は並列数を制限し、未取得の間は Gopher のシルエットを表示する

### ストレージ

v0 は MySQL の BLOB（64×64 PNG は数 KB）。件数が増えたら S3 互換ストレージ（MinIO 等）に移す。

### 表示名の描画

- 表示名の文字種は制限しないが、**フォントに含まれない字は豆腐（□）になる**。日本語を含められるよう Noto Sans JP などの日本語対応フォントを同梱する（サイズ対策としてサブセット化を検討）
- 絵文字はカラー描画が難しいため、v0 では非対応（豆腐表示を許容）。必要になったら絵文字用の画像アトラスを用意する

---

## 9. クライアント（Ebitengine WASM）

### 9.1 描画

- **背景**: オフィス風のマップ画像（タイルマップ＋装飾）を 1 枚の `*ebiten.Image` に事前合成して描画する
- **Gopher**: 青・赤・黄の 3 色分のスプライト（4 方向の歩行アニメーションを想定）。同じ画像を連続で描くと Ebitengine が自動でバッチ化するため、**色ごとにまとめて描画順を整える**（数百体でも `DrawImage` の呼び出しコストが支配的にならないように）
- **名前タグ**: 文字描画は重い。表示名ごとに描画結果をキャッシュ（`*ebiten.Image`）し、毎フレームの `text` 呼び出しを避ける
- **深度順**: y 座標でソートして奥から手前に描く
- 数百体の画面になるため、LOD として「画面が混雑したら名前タグを省略する」切り替えも用意しておく

### 9.2 入力

- キーボード: `W` `A` `S` `D`（`MoveDir`）
- マウス / タッチ: クリックした画面座標をタイル座標に変換して `MoveTo`
- ブラウザの裏タブでは `requestAnimationFrame` が止まる。復帰時は `Welcome` 相当の状態再取得（または `Sync` 待ち）で合わせ直す

### 9.3 Gopher クレジット表記（Must）

Gopher は Renée French さんのデザインで、**CC BY 3.0** ライセンス。色を変える改変を加えるため、改変の旨も明示する。

- **画面中央に常時表示する**（入室前の画面・入室後のマップ上とも。閉じる操作は設けない）
- 表記内容（案）:
  > The Go gopher was designed by Renée French. Licensed under CC BY 3.0 — modified (recolored).
  > https://creativecommons.org/licenses/by/3.0/
- 描画は **最後（最前面）** に行い、アバターや UI に隠れないようにする。視認性のため半透明の背景を付けてもよい（透明度の調整は可、非表示は不可）
- LT のスライドにも同様に、スライドの中央に表記する
- 受け入れ条件: スクリーンショット（入室前・入室後・全員が密集した状態）のすべてで、読める状態で中央にクレジットが見えること

### 9.4 WASM 固有の注意

- `os/exec`、ファイルI/O、生のTCP/UDPソケットをクライアントで使わない（WASM 非対応）
- `game.wasm` は十数 MB になる。gzip / brotli 配信と、読み込み中の表示を入れる
- 変更後は `GOOS=js GOARCH=wasm go build ./cmd/client` が通ることを確認する

---

## 10. インフラ（おうちk3s）

### 前提の確認

`~/src/infra` を確認した時点では **Agones のマニフェストは未導入**（`agones` を含むファイルなし）。Wave Duel の設計書（P3）にも導入予定として書かれているため、**Agones 本体は両プロジェクトで共用**する。Helm values の `gameservers.namespaces` に `wave-duel` と `gopher-office` の両方を指定する。

導入時の注意は Wave Duel の設計書 §9 と同じ:

- ArgoCD Application に `syncOptions: [ServerSideApply=true]`
- k3s のバージョンと Agones の対応 Kubernetes バージョンを事前に照合する
- ノードのアーキテクチャ（amd64 / arm64）を確認し、イメージは multi-arch でビルドする
- 2 ノードの空きリソースを確認する。allocator と ping は使わないので無効化してよい

### 追加するもの

```
apps/
└── gopher-office.yaml
manifests/gopher-office/
├── namespace.yaml
├── fleet.yaml               # Fleet + FleetAutoscaler（containerPort 7654、portPolicy: None）
├── api-deployment.yaml      # container name: office-api
├── api-service.yaml
├── gateway-deployment.yaml  # container name: office-gateway
├── gateway-service.yaml
├── web-deployment.yaml      # container name: office-web
├── rbac.yaml                # api: allocation create / gateway: gameservers get
├── ingress.yaml             # office.o-ga09.com, office.home.local
├── migration-job.yaml       # container name: migration
├── servicemonitor.yaml
└── sealed-secret.yaml
```

- **Fleet**: 常時 1〜2 台を待機させる（`FleetAutoscaler` の Buffer）。上限は小さめに設定する
- **GameServer のリソース**: 200 体のブロードキャストを処理するため、Wave Duel より余裕を持たせる（目安: request `cpu: 200m / memory: 128Mi`、limit `cpu: 1 / memory: 256Mi`。負荷テストで調整）
- **ingress-nginx**: WebSocket 用に `proxy-read-timeout` / `proxy-send-timeout` を 3600 に設定する
- **Cloudflare Tunnel**: Public Hostname に `office.o-ga09.com` → ingress-nginx を追加する

### 注意

`o-ga09/infra` は **main への push が即本番反映**されるため、push 前にユーザーへ確認する。

### 可観測性

- ログ: 標準出力に JSON
- メトリクス: `/metrics`（ServiceMonitor）。接続数、ルーム内人数、tick 処理時間、ブロードキャスト送信バイト数、送信キュー溢れの回数
- 負荷テスト時の帯域確認のため、ルーム内人数と送信バイトのダッシュボードを用意する

---

## 11. 音声・動画通話（LT 後）

### 制約

WebRTC のメディアは UDP（または TCP/TLS の TURN）を使う。**Cloudflare Tunnel は UDP を通せない**ため、メディアを自宅の k3s に流す構成（SFU など）は別経路が必要になる。そこで検証段階は **P2P メッシュ**にする。

### 構成（検証用）

- ブラウザ同士が WebRTC で直接つなぐ。メディアは自宅回線を通らない
- **シグナリング**（SDP / ICE の交換）は既存の GameServer の WebSocket に載せる。`Signal{to, payload}` を GameServer が宛先へ中継するだけ
- STUN は公開のものを使用。NAT 越えに失敗する環境向けの TURN は後回し
- **人数は 4〜6 人程度に限定**する（メッシュは接続数が人数の二乗で増える）。実際に聞こえる範囲を距離で絞る**近接音声**にすれば、同時接続数を抑えられる

### 実装方針

- 通話は**ブラウザ標準の WebRTC**に任せる。Ebitengine（Go/WASM）からは `syscall/js` で、`index.html` に置いた JS の薄いラッパー（`getUserMedia`、`RTCPeerConnection`、`GainNode`）を呼ぶ
- 音量は Gopher 間の距離に応じて `GainNode` で調整する（一定距離外は 0）
- 動画は Ebitengine の canvas への取り込みが重いため、最初は **DOM オーバーレイ**（`<video>` を canvas の上に重ねる）で表示する
- 音声から先に作り、動画はその後に追加する

### 将来の本命構成（メモ）

人数を増やすなら SFU が必要。候補: LiveKit の自前運用、Cloudflare Realtime SFU / TURN。UDP の経路をどう確保するか（ルーターのポート開放 / VPS リレー / マネージド SFU）は、その時点で ADR を起こして決める。

---

## 12. リポジトリ構成

リポジトリ名は仮。現在のワークスペース `~/src/ebiten-workspace` は未 git 管理で、Git 化とリポジトリ名は別途決める。

```
cmd/
├── client/main.go        # Ebiten クライアント（ブラウザ WASM / デスクトップ）
├── gameserver/main.go    # -local フラグで Agones SDK をスキップ
├── gateway/main.go
├── api/main.go
└── bot/main.go           # 負荷テスト用の疑似クライアント（ランダムに歩く）
internal/
├── sim/                  # ebiten 非依存。マップ・衝突・経路探索・移動。★最優先でテスト
├── client/               # 描画・入力・画面遷移（sim を使う）
├── netcode/              # 補間・時刻同期（クライアント側）
├── gameserver/
├── gateway/
└── api/
proto/office/v1/          # メッセージ定義
web/                      # index.html（game.wasm / wasm_exec.js は生成物）
assets/                   # マップ画像、Gopher スプライト、フォント
deploy/local/             # kind/k3d + Agones のローカル検証用（任意）
Dockerfile.*              # 各バイナリ用（distroless / multi-arch）
docs/
```

- `sim` はサーバとクライアントで共有する。時間は整数 tick、乱数はシード付き
- `cmd/bot` は同じ `proto` を使い、WebSocket で入室して歩き回る。「Gopher で埋め尽くす」デモと負荷テストの両方に使う
- 素材（Gopher の画像）の出典とライセンスを `assets/README.md` に記録する

---

## 13. 開発フェーズとスケジュール

2026-11-06 の LT まで約 2 週間。**LT 前（P1〜P4）は、デモに必要なものだけを作る。**

| Phase | 期間（目安） | 内容 | 完了条件 |
|---|---|---|---|
| **P1** | 10/21〜10/26 | `internal/sim`（マップ・衝突・A*・移動）、Gopher 3 色、背景、クレジット表示、ローカルで 1 クライアントが歩ける | ブラウザで WASD とクリック移動ができ、壁を抜けない。クレジットが中央に出ている。sim のテストが通る |
| **P2** | 10/24〜10/30 | proto 定義、`cmd/gameserver`（`-local`）、クライアントのオンライン接続、`cmd/bot`、招待URL入室（API の最小実装：guest / workspace / join） | localhost で 2 つのブラウザと bot 200 体が同じルームに入り、全員が見える。表示名・性別・アイコンが反映される |
| **P3** | 10/29〜11/3 | infra に Agones 導入と Fleet、SDK 組み込み、`cmd/gateway`、Tunnel 公開、`office.o-ga09.com` で動作 | 外部ネットワークのブラウザから招待URLで入れる。無人 5 分で GameServer が Shutdown する |
| **P4** | 11/3〜11/5 | 200 体の負荷試験（帯域・tick 時間）、上限調整、**事前録画**、スライド作成、リハーサル | 本番想定の人数でデモが破綻しない。録画が完成している |
| **LT 当日** | 11/6 19:00〜 | 発表（5〜10 分） | — |
| **P5（LT 後）** | — | P2P 音声通話、近接音声、その後に動画 | [§11](#11-音声動画通話lt-後) |
| **P6（LT 後）** | — | 複数ルーム・複数ワークスペースの UI、永続アカウント、アイコンのオブジェクトストレージ化 | — |

### クリティカルパスと保険

- **最大のリスクは Agones の導入**（クラスタとの相性が未確認、infra に前例がない）。P3 で詰まった場合の保険として、**Agones なしで GameServer を通常の Deployment として動かす構成**（`-local` のまま、ルームは固定の 1 つ）を用意しておく。LT では Agones の構成を図で説明し、実演は保険構成でも成立させる
- 会場の回線が不安定な場合に備え、**事前録画**を用意する（ユーザー確認済みの方針）
- 負荷が不安な場合は、観客参加の上限を下げる（上限は設定で変更可能にしておく）

### LT のストーリー案

1. 何を作ったか（Gopher で埋まるバーチャルオフィス）を見せる
2. Ebitengine WASM と Go のサーバで、`internal/sim` をクライアント・サーバで共有している点
3. ゲームサーバ構成（API とリアルタイムサーバ、Agones）の図
4. 観客にURLを共有して入ってもらう（デモ）
5. 今後（音声通話）

---

## 14. リスク・未決事項

| 項目 | 内容 | 対応 |
|---|---|---|
| Agones の導入 | 未導入。k3s のバージョン・アーキテクチャ・リソースが未確認 | P3 着手前に `kubectl get nodes -o wide` 等で確認。保険構成を用意する |
| 数百人の同期 | 帯域と tick 時間が上限に当たる可能性 | 意図の同期＋1Hz の Sync で見積もり済み。`cmd/bot` で P4 前に早期計測する |
| WASM の描画性能 | 数百体の描画と名前タグで重くなる可能性 | バッチ描画、名前タグのキャッシュ、混雑時の LOD。早めに 200 体で FPS を計測する |
| 会場回線・自宅回線 | 自宅のアップロード帯域がボトルネックになりうる | 事前録画を保険にする。観客の人数を絞る |
| 表示名の自由さ | 文字種を制限しないため、不適切な表示名・画像が出る可能性 | デモ中は招待URLを限定して配る。長さ・制御文字だけ技術的に制限。LT 後に通報・キック機能を検討 |
| アイコン画像 | 不適切画像・巨大画像・細工された画像 | サイズ上限、寸法上限、再エンコード（[§8](#8-プロフィールとアイコン画像)）。モデレーションは LT 後 |
| フォントの字種 | 全字種を許可しても、フォントに無い字は豆腐になる | 日本語フォントを同梱。絵文字は v0 非対応 |
| Gopher のクレジット | 表記漏れはライセンス違反 | Must として受け入れ条件に入れる（[§9.3](#93-gopher-クレジット表記must)）。スライドにも入れる |
| Gopher の素材 | 青・赤・黄の素材を用意する必要がある | 既存 Gopher 画像の色違い加工、または自作。出典とライセンスを記録する |
| Cloudflare の遅延 | 自宅 → Cloudflare → 自宅の往復で数十 ms が加算される | 移動はゲーム性が低く、補間で吸収できる |
| 通話（LT 後） | UDP が Tunnel を通らない。P2P は人数に限界がある | [§11](#11-音声動画通話lt-後)。SFU の経路確保は別 ADR |
| 単一レプリカの API | 割り当ての直列化は Redis ロックで行うが、他は単一前提 | 当面許容 |
| `o-ga09/infra` の push | main への push が即本番反映される | push 前に必ずユーザーに確認する |
