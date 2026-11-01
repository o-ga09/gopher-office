// bot は GameServer に疑似クライアントを大量に接続し、ランダムに歩かせる。
// 負荷試験と、LT デモで画面を Gopher で埋め尽くすために使う。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	officev1 "github.com/o-ga09/gopher-office/gen/office/v1"
	"github.com/o-ga09/gopher-office/internal/netcode"
	"github.com/o-ga09/gopher-office/internal/sim"
)

type stats struct {
	connected atomic.Int64
	failed    atomic.Int64
	messages  atomic.Int64
	moved     atomic.Int64
	syncs     atomic.Int64
	errors    atomic.Int64
}

func main() {
	var (
		server   = flag.String("server", "ws://localhost:7654/room", "GameServer の WebSocket URL")
		token    = flag.String("token", "", "roomToken（-local のサーバでは不要）")
		n        = flag.Int("n", 50, "bot の数")
		ramp     = flag.Duration("ramp", 5*time.Second, "全員を接続し終えるまでの時間（一斉接続を避ける）")
		duration = flag.Duration("duration", 0, "この時間が経ったら終了する。0 なら Ctrl-C まで")
		minWait  = flag.Duration("min-wait", 3*time.Second, "次の行き先を決めるまでの最短の間隔")
		maxWait  = flag.Duration("max-wait", 8*time.Second, "次の行き先を決めるまでの最長の間隔")
		seed     = flag.Uint64("seed", 1, "乱数のシード（同じ値なら同じ動きになる）")
	)
	flag.Parse()
	if *n <= 0 || *maxWait < *minWait {
		log.Fatal("invalid flags: need n > 0 and max-wait >= min-wait")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	tiles := sim.OfficeMap().WalkableTiles()
	var st stats
	var wg sync.WaitGroup
	go report(ctx, &st, *n)

	for i := 0; i < *n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// 接続の時刻を ramp の間にばらす
			delay := time.Duration(int64(*ramp) * int64(i) / int64(*n))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
			runBot(ctx, i, *server, *token, tiles, rand.New(rand.NewPCG(*seed, uint64(i))), *minWait, *maxWait, &st) //nolint:gosec // bot の動きに暗号論的な乱数は不要
		}(i)
	}
	wg.Wait()
	fmt.Printf("done: connected=%d failed=%d messages=%d errors=%d\n", st.connected.Load(), st.failed.Load(), st.messages.Load(), st.errors.Load())
}

func runBot(ctx context.Context, i int, url, token string, tiles []sim.Point, rng *rand.Rand, minWait, maxWait time.Duration, st *stats) {
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	c, err := netcode.Dial(dctx, url, &officev1.Hello{
		RoomToken: token,
		Profile: &officev1.Profile{
			DisplayName: fmt.Sprintf("bot-%03d", i),
			Gender:      officev1.Gender(i % 3), //nolint:gosec // 0〜2 の値
		},
	})
	cancel()
	if err != nil {
		st.failed.Add(1)
		return
	}
	defer c.Close()
	st.connected.Add(1)
	defer st.connected.Add(-1)

	next := time.NewTimer(randDuration(rng, 0, maxWait)) // 最初の動き出しもばらす
	defer next.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-c.Recv():
			if !ok {
				return
			}
			st.messages.Add(1)
			switch {
			case m.GetMoved() != nil:
				st.moved.Add(1)
			case m.GetSync() != nil:
				st.syncs.Add(1)
			case m.GetError() != nil:
				st.errors.Add(1)
				return
			}
		case <-next.C:
			t := tiles[rng.IntN(len(tiles))]
			c.Send(&officev1.ClientMessage{Body: &officev1.ClientMessage_MoveTo{MoveTo: &officev1.MoveTo{X: int32(t.X), Y: int32(t.Y)}}}) //nolint:gosec // タイル座標は小さい
			next.Reset(randDuration(rng, minWait, maxWait))
		}
	}
}

func randDuration(rng *rand.Rand, lo, hi time.Duration) time.Duration {
	if hi <= lo {
		return lo
	}
	return lo + time.Duration(rng.Int64N(int64(hi-lo)))
}

func report(ctx context.Context, st *stats, target int) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	var lastMsgs, lastMoved, lastSyncs int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			msgs, moved, syncs := st.messages.Load(), st.moved.Load(), st.syncs.Load()
			fmt.Printf("connected=%d/%d failed=%d recv/s=%d (moved/s=%d sync/s=%d) errors=%d\n",
				st.connected.Load(), target, st.failed.Load(),
				(msgs-lastMsgs)/5, (moved-lastMoved)/5, (syncs-lastSyncs)/5, st.errors.Load())
			lastMsgs, lastMoved, lastSyncs = msgs, moved, syncs
		}
	}
}
