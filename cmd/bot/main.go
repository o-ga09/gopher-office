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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	officev1 "github.com/o-ga09/gopher-office/gen/office/v1"
	"github.com/o-ga09/gopher-office/internal/apiclient"
	"github.com/o-ga09/gopher-office/internal/netcode"
	"github.com/o-ga09/gopher-office/internal/sim"
)

// failures は、失敗を理由ごとに数える。理由が見えないと、「全員つながらない」ときに原因を切り分けられない。
type failures struct {
	mu sync.Mutex
	n  map[string]int
}

const maxReasonRunes = 160

// add は失敗を、段階（ゲスト登録・入室・WebSocket 接続など）と理由で記録する。
func (f *failures) add(stage string, err error) {
	reason, _, _ := strings.Cut(err.Error(), "\n")
	if r := []rune(reason); len(r) > maxReasonRunes {
		reason = string(r[:maxReasonRunes]) + "…"
	}
	key := stage + ": " + reason
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n == nil {
		f.n = map[string]int{}
	}
	f.n[key]++
}

// top は回数の多い順に、上位 k 件の理由を返す。
func (f *failures) top(k int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := make([]string, 0, len(f.n))
	for key := range f.n {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if f.n[keys[i]] != f.n[keys[j]] {
			return f.n[keys[i]] > f.n[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > k {
		keys = keys[:k]
	}
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i] = fmt.Sprintf("%d x %s", f.n[key], key)
	}
	return out
}

type stats struct {
	connected atomic.Int64
	failed    atomic.Int64
	reasons   failures
	messages  atomic.Int64
	moved     atomic.Int64
	syncs     atomic.Int64
	errors    atomic.Int64
	bytes     atomic.Int64 // 受信した合計バイト数
}

func main() {
	var (
		server   = flag.String("server", "ws://localhost:7654/room", "GameServer の WebSocket URL")
		token    = flag.String("token", "", "roomToken（-local のサーバでは不要）")
		apiBase  = flag.String("api", "", "API のベース URL（指定すると、各 bot がゲスト登録して招待コードで入室する）")
		invite   = flag.String("invite", "", "招待コード（-api と組み合わせる）")
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
	if (*apiBase == "") != (*invite == "") {
		log.Fatal("-api and -invite must be specified together")
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
			runBot(ctx, i, target{server: *server, token: *token, api: *apiBase, invite: *invite}, tiles, rand.New(rand.NewPCG(*seed, uint64(i))), *minWait, *maxWait, &st) //nolint:gosec // bot の動きに暗号論的な乱数は不要
		}(i)
	}
	wg.Wait()
	fmt.Printf("done: connected=%d failed=%d messages=%d errors=%d\n", st.connected.Load(), st.failed.Load(), st.messages.Load(), st.errors.Load())
	printFailures(&st, 10)
}

// printFailures は、失敗の理由を回数の多い順に表示する。
func printFailures(st *stats, k int) {
	for _, line := range st.reasons.top(k) {
		fmt.Println("  failure:", line)
	}
}

// fail は接続の失敗を数えて、理由を記録する。
func (s *stats) fail(stage string, err error) {
	s.failed.Add(1)
	s.reasons.add(stage, err)
}

// target は bot のつなぎ先。api と invite があれば API 経由で入室し、無ければ server に直接つなぐ。
type target struct {
	server, token string
	api, invite   string
}

func runBot(ctx context.Context, i int, tg target, tiles []sim.Point, rng *rand.Rand, minWait, maxWait time.Duration, st *stats) {
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	name, gender := fmt.Sprintf("bot-%03d", i), uint8(i%3) //nolint:gosec // 0〜2 の値

	url, token := tg.server, tg.token
	if tg.api != "" {
		cl := apiclient.New(tg.api)
		if _, err := cl.CreateGuest(dctx, name, gender); err != nil {
			cancel()
			st.fail("create guest", err)
			return
		}
		j, err := cl.Join(dctx, tg.invite)
		if err != nil {
			cancel()
			st.fail("join", err)
			return
		}
		// ゲートウェイは接続先を決めるために URL の token を見る。付け忘れると 401 で全員つながらない
		url, token = j.DialURL(), j.RoomToken
	}
	c, err := netcode.Dial(dctx, url, &officev1.Hello{
		RoomToken: token,
		Profile:   &officev1.Profile{DisplayName: name, Gender: officev1.Gender(gender)},
	})
	cancel()
	if err != nil {
		st.fail("websocket", err)
		return
	}
	defer c.Close()
	st.connected.Add(1)
	defer st.connected.Add(-1)
	var counted int64
	flush := func() { n := c.BytesRecv(); st.bytes.Add(n - counted); counted = n }
	defer flush()
	bytesTick := time.NewTicker(time.Second)
	defer bytesTick.Stop()

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
				st.reasons.add("server rejected", fmt.Errorf("%v: %s", m.GetError().GetCode(), m.GetError().GetMessage()))
				return
			}
		case <-bytesTick.C:
			flush()
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
	var lastMsgs, lastMoved, lastSyncs, lastBytes int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			msgs, moved, syncs := st.messages.Load(), st.moved.Load(), st.syncs.Load()
			bytes := st.bytes.Load()
			conn := max(st.connected.Load(), 1)
			perSec := float64(bytes-lastBytes) / 5
			fmt.Printf("connected=%d/%d failed=%d recv/s=%d (moved/s=%d sync/s=%d) errors=%d  recv=%.2f MB/s total, %.1f KB/s per client (%.1f Mbps total)\n",
				st.connected.Load(), target, st.failed.Load(),
				(msgs-lastMsgs)/5, (moved-lastMoved)/5, (syncs-lastSyncs)/5, st.errors.Load(),
				perSec/1e6, perSec/float64(conn)/1e3, perSec*8/1e6)
			lastMsgs, lastMoved, lastSyncs, lastBytes = msgs, moved, syncs, bytes
			printFailures(st, 3)
		}
	}
}
