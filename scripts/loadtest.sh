#!/bin/sh
# ローカルで GameServer（-local）を起動し、bot を N 体つないで、帯域と tick 時間を測る。
#   使い方: scripts/loadtest.sh [N=200] [秒=60]
# 結果は bot の出力（受信帯域）と、GameServer の stats ログ（送信帯域・tick の処理時間）に出る。
set -eu
N=${1:-200}
SECS=${2:-60}
PORT=${PORT:-17654}
DIR=$(mktemp -d)
trap 'kill "$GS" 2>/dev/null || true; rm -rf "$DIR"' EXIT

go build -o "$DIR/gameserver" ./cmd/gameserver
go build -o "$DIR/bot" ./cmd/bot

# 人数の上限は、試す人数より大きくしておく
"$DIR/gameserver" -local -addr "127.0.0.1:$PORT" -max-players $((N + 100)) >"$DIR/gs.log" 2>&1 &
GS=$!
sleep 1
"$DIR/bot" -server "ws://127.0.0.1:$PORT/room" -n "$N" -ramp 10s -duration "${SECS}s" | tail -n 4
echo "--- GameServer"
grep '"stats"' "$DIR/gs.log" | tail -n 3
