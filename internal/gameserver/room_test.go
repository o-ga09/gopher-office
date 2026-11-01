package gameserver

import (
	"io"
	"log/slog"
	"testing"
	"time"

	officev1 "github.com/o-ga09/gopher-office/gen/office/v1"
	"github.com/o-ga09/gopher-office/internal/sim"
	"github.com/o-ga09/gopher-office/internal/wire"
)

func newTestRoom(max int) *Room {
	return NewRoom(Config{
		RoomID:     "test",
		MaxPlayers: max,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func joinTestConn(t *testing.T, r *Room, id string) *conn {
	t.Helper()
	c := newConn(id, &officev1.Profile{DisplayName: id})
	if res := r.join(c); res.full {
		t.Fatalf("join(%s) unexpectedly full", id)
	}
	return c
}

// drain は送信キューに溜まったメッセージをすべて取り出して復号する。
func drain(t *testing.T, c *conn) []*officev1.ServerMessage {
	t.Helper()
	var msgs []*officev1.ServerMessage
	for {
		select {
		case b := <-c.out:
			m, err := wire.UnmarshalServer(b)
			if err != nil {
				t.Fatal(err)
			}
			msgs = append(msgs, m)
		default:
			return msgs
		}
	}
}

func TestJoinSendsWelcomeAndNotifiesOthers(t *testing.T) {
	r := newTestRoom(10)
	a := joinTestConn(t, r, "a")

	msgs := drain(t, a)
	if len(msgs) != 1 || msgs[0].GetWelcome() == nil {
		t.Fatalf("a: want one Welcome, got %v", msgs)
	}
	w := msgs[0].GetWelcome()
	if w.GetPlayerId() != "a" || w.GetTickRate() != sim.TickRate || w.GetMaxPlayers() != 10 || len(w.GetPlayers()) != 1 {
		t.Fatalf("unexpected welcome: %v", w)
	}

	b := joinTestConn(t, r, "b")
	if got := drain(t, b)[0].GetWelcome(); len(got.GetPlayers()) != 2 {
		t.Fatalf("b's welcome must list everyone, got %d", len(got.GetPlayers()))
	}
	joined := drain(t, a)
	if len(joined) != 1 || joined[0].GetPlayerJoined().GetPlayer().GetId() != "b" {
		t.Fatalf("a must be told that b joined, got %v", joined)
	}
	if r.Players() != 2 {
		t.Fatalf("Players() = %d", r.Players())
	}
}

func TestJoinFull(t *testing.T) {
	r := newTestRoom(1)
	joinTestConn(t, r, "a")
	if res := r.join(newConn("b", &officev1.Profile{})); !res.full {
		t.Fatal("second player must be rejected as full")
	}
	if r.Players() != 1 {
		t.Fatalf("Players() = %d, want 1", r.Players())
	}
}

func TestRejoinWithSameIDReplacesOldConnection(t *testing.T) {
	r := newTestRoom(1) // 満員でも、同じ ID の再接続は入れ替えで通る
	old := joinTestConn(t, r, "a")
	fresh := joinTestConn(t, r, "a")

	select {
	case <-old.done:
	default:
		t.Fatal("old connection must be shut down")
	}
	if r.Players() != 1 || r.players["a"] != fresh {
		t.Fatal("the new connection must own the slot")
	}
	// 古い接続の退室通知は、新しい接続を巻き込まない
	r.handle(leaveCmd{c: old})
	if r.Players() != 1 {
		t.Fatal("stale leave must be ignored")
	}
}

func TestLeaveNotifiesOthers(t *testing.T) {
	r := newTestRoom(10)
	a := joinTestConn(t, r, "a")
	b := joinTestConn(t, r, "b")
	drain(t, a)
	drain(t, b)

	r.handle(leaveCmd{c: b})
	left := drain(t, a)
	if len(left) != 1 || left[0].GetPlayerLeft().GetPlayerId() != "b" {
		t.Fatalf("want PlayerLeft(b), got %v", left)
	}
	if r.Players() != 1 {
		t.Fatalf("Players() = %d", r.Players())
	}
}

func moveTo(x, y int32) *officev1.ClientMessage {
	return &officev1.ClientMessage{Body: &officev1.ClientMessage_MoveTo{MoveTo: &officev1.MoveTo{X: x, Y: y}}}
}

func moveDir(dx, dy int32) *officev1.ClientMessage {
	return &officev1.ClientMessage{Body: &officev1.ClientMessage_MoveDir{MoveDir: &officev1.MoveDir{Dx: dx, Dy: dy}}}
}

func TestMoveToBroadcastsPreStepStateThenMoves(t *testing.T) {
	r := newTestRoom(10)
	a := joinTestConn(t, r, "a")
	b := joinTestConn(t, r, "b")
	drain(t, a)
	drain(t, b)

	startX, startY := a.actor.X, a.actor.Y
	r.handle(inputCmd{c: a, msg: moveTo(2, 2)})
	r.step(time.Now())

	for _, c := range []*conn{a, b} { // 本人にも他人にも届く
		msgs := drain(t, c)
		if len(msgs) != 1 || msgs[0].GetMoved() == nil {
			t.Fatalf("%s: want one Moved, got %v", c.id, msgs)
		}
		s := msgs[0].GetMoved().GetStates()[0]
		if s.GetPlayerId() != "a" || !s.GetHasTarget() || s.GetTargetX() != 2 || s.GetTargetY() != 2 {
			t.Fatalf("unexpected state: %v", s)
		}
		// 状態は Step の前の位置（クライアントが同じ軌跡を再生するため）
		if int(s.GetX()) != startX || int(s.GetY()) != startY {
			t.Fatalf("state must be the pre-step position, got (%d,%d) want (%d,%d)", s.GetX(), s.GetY(), startX, startY)
		}
	}
	if a.actor.X == startX && a.actor.Y == startY {
		t.Fatal("actor must have moved after the step")
	}
}

func TestMovedStateReplaysToTheSameTrajectory(t *testing.T) {
	r := newTestRoom(10)
	a := joinTestConn(t, r, "a")
	drain(t, a)

	r.handle(inputCmd{c: a, msg: moveTo(30, 3)})
	r.step(time.Now())
	s := drain(t, a)[0].GetMoved().GetStates()[0]

	replay := wire.ActorFromState(r.cfg.Map, s)
	replay.Step(r.cfg.Map) // サーバは Moved を送った後に 1 回 Step している
	for i := 0; i < 100; i++ {
		r.step(time.Now())
		replay.Step(r.cfg.Map)
		if a.actor.X != replay.X || a.actor.Y != replay.Y {
			t.Fatalf("tick %d: server (%d,%d) != replay (%d,%d)", i, a.actor.X, a.actor.Y, replay.X, replay.Y)
		}
	}
}

func TestInvalidMoveIsIgnored(t *testing.T) {
	r := newTestRoom(10)
	a := joinTestConn(t, r, "a")
	drain(t, a)

	r.handle(inputCmd{c: a, msg: moveTo(0, 0)})    // 壁
	r.handle(inputCmd{c: a, msg: moveTo(-5, 999)}) // 範囲外
	r.handle(inputCmd{c: a, msg: moveDir(0, 0)})   // 止まっているのに停止
	r.step(time.Now())
	if msgs := drain(t, a); len(msgs) != 0 {
		t.Fatalf("invalid inputs must not produce messages, got %v", msgs)
	}
}

func TestMoveDirMovesAndStopIsBroadcast(t *testing.T) {
	r := newTestRoom(10)
	a := joinTestConn(t, r, "a")
	drain(t, a)

	r.handle(inputCmd{c: a, msg: moveDir(1, 0)})
	r.step(time.Now())
	if s := drain(t, a)[0].GetMoved().GetStates()[0]; s.GetDirX() != 1 {
		t.Fatalf("dir = %d", s.GetDirX())
	}
	r.handle(inputCmd{c: a, msg: moveDir(0, 0)})
	r.step(time.Now())
	if s := drain(t, a)[0].GetMoved().GetStates()[0]; s.GetDirX() != 0 || s.GetDirY() != 0 {
		t.Fatalf("stop must be broadcast, got %v", s)
	}
}

func TestSyncEveryOneSecond(t *testing.T) {
	r := newTestRoom(10)
	a := joinTestConn(t, r, "a")
	joinTestConn(t, r, "b")
	drain(t, a)

	var syncs int
	for i := 0; i < syncEvery*3; i++ {
		r.step(time.Now())
	}
	for _, m := range drain(t, a) {
		if s := m.GetSync(); s != nil {
			syncs++
			if len(s.GetStates()) != 2 {
				t.Fatalf("sync must carry everyone, got %d", len(s.GetStates()))
			}
		}
	}
	if syncs != 3 {
		t.Fatalf("syncs = %d, want 3 over 3 seconds", syncs)
	}
}

func TestSlowConsumerIsKickedButSyncIsOnlyDropped(t *testing.T) {
	r := newTestRoom(10)
	slow := joinTestConn(t, r, "slow")
	drain(t, slow)

	// Sync だけが溜まっても追い出さない（捨てる）
	for i := 0; i < syncEvery*(outBuffer+10); i++ {
		r.step(time.Now())
	}
	if slow.kicked {
		t.Fatal("dropping Sync must not kick the consumer")
	}

	// 捨てられないメッセージで溢れたら追い出す
	other := joinTestConn(t, r, "other")
	drain(t, other)
	for i := 0; i < outBuffer+10 && !slow.kicked; i++ {
		r.handle(inputCmd{c: other, msg: moveDir(1, 0)})
		r.step(time.Now())
		r.handle(inputCmd{c: other, msg: moveDir(0, 0)})
		r.step(time.Now())
		drain(t, other)
	}
	if !slow.kicked {
		t.Fatal("a consumer that cannot keep up must be kicked")
	}
	select {
	case <-slow.done:
	default:
		t.Fatal("kicked connection must be shut down")
	}
}

func TestIdleFiresOnceAfterTimeout(t *testing.T) {
	var fired int
	r := NewRoom(Config{IdleTimeout: time.Minute, OnIdle: func() { fired++ }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	r.emptySince = time.Now()

	r.step(r.emptySince.Add(30 * time.Second))
	if fired != 0 {
		t.Fatal("must not fire before the timeout")
	}
	r.step(r.emptySince.Add(2 * time.Minute))
	r.step(r.emptySince.Add(3 * time.Minute))
	if fired != 1 {
		t.Fatalf("fired = %d, want exactly once", fired)
	}
}

func TestIdleNotFiredWhileOccupied(t *testing.T) {
	var fired int
	r := NewRoom(Config{IdleTimeout: time.Minute, OnIdle: func() { fired++ }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	r.emptySince = time.Now()
	c := joinTestConn(t, r, "a")
	r.step(r.emptySince.Add(10 * time.Minute))
	if fired != 0 {
		t.Fatal("an occupied room is not idle")
	}
	r.handle(leaveCmd{c: c})
	r.step(r.emptySince.Add(2 * time.Minute)) // 退室時刻から数える
	if fired != 1 {
		t.Fatalf("fired = %d after the room emptied", fired)
	}
}

func TestSpawnTilesAreWalkableAndScattered(t *testing.T) {
	r := newTestRoom(500)
	seen := map[sim.Point]bool{}
	for i := 0; i < 200; i++ {
		c := joinTestConn(t, r, string(rune('A'+i%26))+string(rune('a'+i/26)))
		p := c.actor.Tile()
		if !r.cfg.Map.Walkable(p.X, p.Y) {
			t.Fatalf("spawned on a non-walkable tile %v", p)
		}
		if seen[p] && i != 0 {
			t.Fatalf("player %d spawned on an occupied tile %v", i, p)
		}
		seen[p] = true
	}
	first := newTestRoom(5)
	if got := joinTestConn(t, first, "x").actor.Tile(); got != sim.OfficeSpawn {
		t.Fatalf("first player must spawn at the office spawn, got %v", got)
	}
}

func TestRateLimiter(t *testing.T) {
	l := newRateLimiter(10, 5)
	now := time.Now()
	for i := 0; i < 5; i++ {
		if !l.allow(now) {
			t.Fatalf("burst message %d must be allowed", i)
		}
	}
	if l.allow(now) {
		t.Fatal("beyond the burst must be rejected")
	}
	if !l.allow(now.Add(200 * time.Millisecond)) { // 10/s なので 0.2 秒で 2 トークン戻る
		t.Fatal("tokens must refill over time")
	}
}

func TestDeferredIdleCountsFromArm(t *testing.T) {
	var fired int
	r := NewRoom(Config{IdleTimeout: time.Minute, DeferIdle: true, OnIdle: func() { fired++ }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	r.emptySince = time.Now().Add(-time.Hour) // 待機中にいくら経っても、数えない

	r.step(time.Now().Add(time.Hour))
	if fired != 0 {
		t.Fatal("an unallocated server must never go idle")
	}

	r.Arm()
	r.step(time.Now().Add(30 * time.Second))
	if fired != 0 {
		t.Fatal("must count from the allocation, not from the start")
	}
	r.step(time.Now().Add(2 * time.Minute))
	if fired != 1 {
		t.Fatalf("fired = %d, want 1", fired)
	}
}
