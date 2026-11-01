# LT 当日の手順（2026-11-06）

デモの準備・当日の進行・うまくいかないときの切り替え。数値の根拠は [設計書 §6](design.md#実測p4) を参照。

## 計測結果（P4）

ローカル（`make loadtest`）で、bot が 3〜8 秒ごとに行き先を変えて歩く条件。

| | 送信帯域（GameServer 全体） | 1 人あたり | tick 平均 |
|---|---|---|---|
| 変更前（200 体） | 11.7 Mbps | 7.3 KB/s | 1.4 ms |
| `pid` 化 + 静止中を Sync から省く | 5.2 Mbps | 3.2 KB/s | 1.1 ms |
| + Sync を人数に応じて間引く | **3.2 Mbps** | 1.8 KB/s | 1.3 ms |
| 400 体（上限を 600 に上げて） | 8.0 Mbps | 2.5 KB/s | 1.0 ms |

- tick の予算は 50 ms（20Hz）。200 体でも 3% 未満で、**制約は CPU ではなく自宅の上り回線**
- 切断・追い出し（`kicked`）・捨てたメッセージ（`dropped`）・tick の超過（`tick_overruns`）は、すべて 0
- 本番は Cloudflare Tunnel を挟むので、遅延が増える。帯域の目安は上のとおりで、**観客 50 人 + bot 150 体なら 3 Mbps 前後**
- 人数の上限は既定 200（`-max-players`）。超えた人には「満員」が出る

## 前日までに

- [ ] `https://office.o-ga09.com/` が開き、オフラインで歩ける（Web 配信の確認）
- [ ] 招待 URL からオンラインで入れる（下の「ワークスペースを作る」）。**外のネットワーク（スマホのモバイル回線など）からも確認する**
- [ ] 無人 5 分で GameServer が Shutdown し、次の入室で新しく割り当たる（`kubectl -n gopher-office get gs -w`）
- [ ] bot 150 体で入る（下の「bot で埋める」）。人数が `Online N/200` に出る
- [ ] **事前録画**を撮る（下の「録画」）。ファイルを 2 か所に保存する
- [ ] 発表用 PC で、ブラウザのキャッシュを消した状態から入れる（`game.wasm` は十数 MB）

## ワークスペースを作る（招待 URL）

```bash
kubectl -n gopher-office exec deploy/office-api -- \
  /ctl -api http://localhost:8080 -base-url https://office.o-ga09.com -name "LT"
# → https://office.o-ga09.com/w/XXXXXXXXXXXX
```

招待コードは URL を知っている人なら誰でも入れる。LT のあとは、ワークスペースの招待を使わなくなったら放置でよい（無人 5 分で GameServer は消える）。

## bot で埋める

bot は、1 体ずつゲストを登録して入室する。**API はゲスト作成を IP ごとに 20 回/分に制限している**ので、1 台から 150 体を入れるなら、入室に **8 分かける**（`-ramp 8m`）。

```bash
go run ./cmd/bot -api https://office.o-ga09.com -invite XXXXXXXXXXXX -n 150 -ramp 8m
```

- 発表の **15 分前**に始める。bot は Ctrl-C まで入ったまま歩き続ける（`-duration 0`）
- 制限を一時的に上げたいときは、infra の `api-deployment.yaml` に `-guest-per-minute=300` を足す。**終わったら必ず戻す**（上げたままだと、ゲストの大量作成を防げない）
- bot が `failed` を出すときは、制限（`rate_limited`）か、満員（`full`）。`kubectl -n gopher-office logs deploy/office-api` で見る
- 停止は Ctrl-C。bot が全員抜けると、無人 5 分で GameServer が終了する

## 当日の進行（5〜10 分）

[設計書 §13 のストーリー案](design.md#lt-のストーリー案)に沿う。

1. 画面を Gopher で埋めた状態を見せる（bot 150 体）
2. `internal/sim` をクライアント（WASM）とサーバで共有している点
3. 構成図（API / gateway / GameServer / Agones）
4. **観客に URL を共有して入ってもらう**。人数が増えるのを見せる
5. 今後（音声通話）

クレジット（Renée French、CC BY 3.0）は画面中央に常時出る。**スクリーンショットや録画でも隠さない**。スライドにも同じ表記を入れる。

## 録画

- 発表用 PC の画面を、**bot 150 体が入った状態**で 1〜2 分録る。歩く・観客が入る（別端末で入る）・人数が増える、まで
- 解像度は発表用のプロジェクタに合わせる。クレジットが読めることを確認する
- 録画は**本番と同じ URL**（`office.o-ga09.com`）で撮ると、スライドの中で「実際に動いている」と言える

## うまくいかないとき

| 症状 | 切り替え |
|---|---|
| 会場の回線が不安定で、オンラインが重い | **事前録画**を流す。オフライン（`https://office.o-ga09.com/`）は、回線が弱くても歩ける |
| 観客が入りすぎて重い | 満員（200）になれば自動で断る。それでも重いなら、bot を止めて人数を減らす |
| 入室できない（「No room is available」） | Fleet に待機（Ready）の GameServer が無い。`kubectl -n gopher-office get fleet,gs`。`FleetAutoscaler` の `minReplicas` を上げる |
| Agones まわりが動かない | 保険構成（`-local` の GameServer を 1 台、通常の Deployment で動かす）。LT では構成図で説明する |
| 招待 URL が開けない | `https://office.o-ga09.com/?online=1` は使えない（API 経由の入室のみ）。事前録画に切り替える |

## リハーサル

- [ ] 時間を計って通す（5〜10 分に収まるか）
- [ ] 発表用の PC・回線（モバイルルータなど）で、本番と同じ手順を 1 回通す
- [ ] 観客役の端末を 2〜3 台用意して、URL から入れることを確認する
- [ ] 録画に切り替える操作を練習する
