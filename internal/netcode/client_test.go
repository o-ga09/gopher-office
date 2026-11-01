package netcode_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	officev1 "github.com/o-ga09/gopher-office/gen/office/v1"
	"github.com/o-ga09/gopher-office/internal/gameserver"
	"github.com/o-ga09/gopher-office/internal/netcode"
	"github.com/o-ga09/gopher-office/internal/sim"
)

func startServer(t *testing.T, maxPlayers int) string {
	t.Helper()
	s := gameserver.NewServer(gameserver.Config{
		MaxPlayers: maxPlayers,
		Verifier:   gameserver.InsecureVerifier{},
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Room().Run(ctx); close(done) }()
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		ts.Close()
		cancel()
		<-done
	})
	return "ws" + strings.TrimPrefix(ts.URL, "http") + "/room"
}

// participant は 1 人分のクライアント（接続と世界）。
type participant struct {
	c *netcode.Client
	w *netcode.World
}

func join(t *testing.T, url, name string) *participant {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := netcode.Dial(ctx, url, &officev1.Hello{Profile: &officev1.Profile{DisplayName: name, Gender: officev1.Gender_GENDER_FEMALE}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return &participant{c: c, w: netcode.NewWorld(sim.OfficeMap())}
}

// pump は届いているメッセージを世界に反映する。
func (p *participant) pump(t *testing.T) error {
	t.Helper()
	for {
		select {
		case m, ok := <-p.c.Recv():
			if !ok {
				return p.c.Err()
			}
			if err := p.w.Apply(m); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

// run は 20Hz で、受信の反映とシミュレーションを d の間だけ回す。
func run(t *testing.T, d time.Duration, ps ...*participant) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		for _, p := range ps {
			if err := p.pump(t); err != nil {
				t.Fatal(err)
			}
			p.w.Step()
		}
		time.Sleep(time.Second / sim.TickRate)
	}
}

// runUntil は cond が成り立つか、timeout になるまで世界を回す。
func runUntil(t *testing.T, timeout time.Duration, cond func() bool, ps ...*participant) {
	t.Helper()
	end := time.Now().Add(timeout)
	for time.Now().Before(end) && !cond() {
		run(t, time.Second/sim.TickRate, ps...)
	}
}

func TestTwoWorldsConverge(t *testing.T) {
	url := startServer(t, 10)
	a := join(t, url, "alice")
	run(t, 300*time.Millisecond, a)
	if !a.w.Ready {
		t.Fatal("alice must receive Welcome")
	}
	b := join(t, url, "bob")
	run(t, 300*time.Millisecond, a, b)
	if len(a.w.Players) != 2 || len(b.w.Players) != 2 {
		t.Fatalf("both must see two players: a=%d b=%d", len(a.w.Players), len(b.w.Players))
	}

	// alice が歩く（自分は予測、bob はサーバ経由の意図から再生する）
	target := sim.Point{X: 30, Y: 3}
	if !a.w.MoveSelfTo(target) {
		t.Fatal("alice cannot move")
	}
	a.c.Send(&officev1.ClientMessage{Body: &officev1.ClientMessage_MoveTo{MoveTo: &officev1.MoveTo{X: int32(target.X), Y: int32(target.Y)}}}) //nolint:gosec // テスト用の小さなタイル座標
	run(t, 1500*time.Millisecond, a, b)

	// Sync で他人の Actor は作り直されることがあるため、毎回世界から取り直す
	aliceOnA := func() *sim.Actor { return a.w.Self().Actor }
	aliceOnB := func() *sim.Actor { return b.w.Players[a.w.SelfID].Actor }
	if !aliceOnA().Moving() || !aliceOnB().Moving() {
		t.Fatal("alice must still be walking on both screens")
	}
	// 往復遅延ぶんの誤差（ローカルでは数 tick）の範囲で、b の画面の alice は a の予測に追従している
	if drift := max(abs(aliceOnA().X-aliceOnB().X), abs(aliceOnA().Y-aliceOnB().Y)); drift > 2*sim.SubPerTile {
		t.Fatalf("worlds diverged: drift = %d sub-units", drift)
	}

	// 到着後は完全に一致する（同じ sim で同じ目的地に着く）
	runUntil(t, 12*time.Second, func() bool { return !aliceOnA().Moving() && !aliceOnB().Moving() }, a, b)
	if aliceOnA().Moving() || aliceOnB().Moving() {
		t.Fatalf("alice must have arrived: a=%v b=%v", aliceOnA().Moving(), aliceOnB().Moving())
	}
	if aliceOnA().X != aliceOnB().X || aliceOnA().Y != aliceOnB().Y {
		t.Fatalf("final positions differ: a=(%d,%d) b=(%d,%d)", aliceOnA().X, aliceOnA().Y, aliceOnB().X, aliceOnB().Y)
	}
	if aliceOnA().Tile() != target {
		t.Fatalf("alice ended at %v, want %v", aliceOnA().Tile(), target)
	}
}

func TestFullRoomSurfacesAsServerError(t *testing.T) {
	url := startServer(t, 1)
	a := join(t, url, "a")
	run(t, 300*time.Millisecond, a)

	b := join(t, url, "b")
	var err error
	for i := 0; i < 50 && err == nil; i++ {
		err = b.pump(t)
		time.Sleep(20 * time.Millisecond)
	}
	se, ok := err.(*netcode.ServerError) //nolint:errorlint // pump は *ServerError をそのまま返す
	if !ok || se.Code != officev1.ErrorCode_ERROR_CODE_FULL {
		t.Fatalf("err = %v, want a FULL server error", err)
	}
}

func TestPingMeasuresRTT(t *testing.T) {
	url := startServer(t, 10)
	a := join(t, url, "a")
	run(t, 1500*time.Millisecond, a) // Ping は 1 秒ごと。接続が生きていて Pong が処理されている
	if a.c.Err() != nil {
		t.Fatalf("connection must stay alive: %v", a.c.Err())
	}
	if rtt := a.c.RTT(); rtt < 0 || rtt > time.Second {
		t.Fatalf("rtt = %v, want within [0, 1s] on localhost", rtt)
	}
}

func TestCloseEndsTheStream(t *testing.T) {
	url := startServer(t, 10)
	a := join(t, url, "a")
	run(t, 200*time.Millisecond, a)
	a.c.Close()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-a.c.Recv():
			if !ok {
				if a.c.Err() == nil {
					t.Fatal("Err must explain why the stream ended")
				}
				return
			}
		case <-deadline:
			t.Fatal("Recv must be closed after Close")
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
