package netcode

import (
	"errors"
	"hash/fnv"
	"testing"

	officev1 "github.com/o-ga09/gopher-office/gen/office/v1"
	"github.com/o-ga09/gopher-office/internal/sim"
	"github.com/o-ga09/gopher-office/internal/wire"
)

// pid はテスト用に、ID から決まる番号を作る（ID が違えば番号も違う）。
func pid(id string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return h.Sum32()
}

func idleState(id string, x, y int) *officev1.MoveState {
	return &officev1.MoveState{Pid: pid(id), X: int32(x), Y: int32(y)} //nolint:gosec // テスト用の小さな座標
}

func player(id, name string, g officev1.Gender, s *officev1.MoveState) *officev1.Player {
	return &officev1.Player{Id: id, Pid: pid(id), Profile: &officev1.Profile{DisplayName: name, Gender: g}, State: s}
}

func welcome(self string, players ...*officev1.Player) *officev1.ServerMessage {
	return &officev1.ServerMessage{Body: &officev1.ServerMessage_Welcome{Welcome: &officev1.Welcome{
		PlayerId: self, SelfPid: pid(self), Tick: 5, TickRate: sim.TickRate, MaxPlayers: 200, Players: players,
	}}}
}

func moved(states ...*officev1.MoveState) *officev1.ServerMessage {
	return &officev1.ServerMessage{Body: &officev1.ServerMessage_Moved{Moved: &officev1.Moved{Tick: 9, States: states}}}
}

func syncMsg(states ...*officev1.MoveState) *officev1.ServerMessage {
	return &officev1.ServerMessage{Body: &officev1.ServerMessage_Sync{Sync: &officev1.Sync{Tick: 20, States: states}}}
}

func apply(t *testing.T, w *World, m *officev1.ServerMessage) {
	t.Helper()
	if err := w.Apply(m); err != nil {
		t.Fatal(err)
	}
}

func center(tile int) int { return tile*sim.SubPerTile + sim.SubPerTile/2 }

func newWorldWithTwo(t *testing.T) *World {
	t.Helper()
	w := NewWorld(sim.OfficeMap())
	apply(t, w, welcome("me",
		player("me", "me", officev1.Gender_GENDER_FEMALE, idleState("me", center(20), center(19))),
		player("you", "you", officev1.Gender_GENDER_MALE, idleState("you", center(22), center(19))),
	))
	return w
}

func TestWelcomeBuildsTheWorld(t *testing.T) {
	w := newWorldWithTwo(t)
	if !w.Ready || w.SelfID != "me" || w.MaxPlayers != 200 || len(w.Players) != 2 {
		t.Fatalf("unexpected world: ready=%v self=%q max=%d players=%d", w.Ready, w.SelfID, w.MaxPlayers, len(w.Players))
	}
	if self := w.Self(); self.Name != "me" || self.Gender != sim.GenderFemale {
		t.Fatalf("self = %+v", self)
	}
	if w.Players["you"].Gender != sim.GenderMale {
		t.Fatal("gender must come from the profile")
	}
}

func TestWelcomeWithoutSelfIsNotReady(t *testing.T) {
	w := NewWorld(sim.OfficeMap())
	apply(t, w, welcome("ghost", player("other", "o", officev1.Gender_GENDER_MALE, idleState("other", 0, 0))))
	if w.Ready {
		t.Fatal("a world without self must not be ready")
	}
}

func TestJoinAndLeave(t *testing.T) {
	w := newWorldWithTwo(t)
	apply(t, w, &officev1.ServerMessage{Body: &officev1.ServerMessage_PlayerJoined{PlayerJoined: &officev1.PlayerJoined{
		Player: player("new", "new", officev1.Gender_GENDER_OTHER, idleState("new", center(5), center(15))),
	}}})
	if len(w.Players) != 3 || w.Players["new"].Gender != sim.GenderOther {
		t.Fatal("PlayerJoined must add a player")
	}
	apply(t, w, &officev1.ServerMessage{Body: &officev1.ServerMessage_PlayerLeft{PlayerLeft: &officev1.PlayerLeft{Pid: pid("you")}}})
	if len(w.Players) != 2 || w.Players["you"] != nil {
		t.Fatal("PlayerLeft must remove the player")
	}
}

func TestMovedReplaysOthersButNotSelf(t *testing.T) {
	w := newWorldWithTwo(t)
	selfX := w.Self().Actor.X

	other := idleState("you", center(22), center(19))
	other.HasTarget, other.TargetX, other.TargetY = true, 2, 2
	selfMove := idleState("me", center(1), center(1)) // 無視される（予測済み）
	selfMove.DirX = 1
	apply(t, w, moved(other, selfMove))

	if !w.Players["you"].Actor.Moving() {
		t.Fatal("other must start moving")
	}
	if w.Self().Actor.X != selfX || w.Self().Actor.Moving() {
		t.Fatal("Moved for self must be ignored")
	}
}

func TestStepInterpolationBase(t *testing.T) {
	w := newWorldWithTwo(t)
	w.SetSelfDir(1, 0)
	before := w.Self().Actor.X
	w.Step()
	self := w.Self()
	if self.PrevX != before || self.Actor.X != before+sim.SpeedSub {
		t.Fatalf("prev=%d x=%d (before=%d)", self.PrevX, self.Actor.X, before)
	}
	x, _ := self.RenderPos(0.5)
	if want := float64(before) + sim.SpeedSub/2.0; x != want {
		t.Fatalf("render x = %v, want %v", x, want)
	}
}

func TestSyncSmallDriftOfOthersKeepsThePath(t *testing.T) {
	w := newWorldWithTwo(t)
	you := w.Players["you"]
	you.Actor = wire.ActorFromState(w.Map, withTarget(idleState("you", center(22), center(19)), 2, 2))
	you.Actor.Step(w.Map)
	x, y := you.Actor.X, you.Actor.Y

	s := withTarget(idleState("you", x+10, y), 2, 2) // 同じ意図、小さなずれ
	apply(t, w, syncMsg(s))
	if you.Actor.X != x || you.Actor.Y != y {
		t.Fatal("a small drift must not move the actor")
	}
	if _, ok := you.Actor.Target(); !ok {
		t.Fatal("the path must be kept")
	}
}

func TestSyncLargeDriftOfOthersSnapsButKeepsIntent(t *testing.T) {
	w := newWorldWithTwo(t)
	you := w.Players["you"]
	you.Actor = wire.ActorFromState(w.Map, withTarget(idleState("you", center(22), center(19)), 2, 2))

	s := withTarget(idleState("you", center(26), center(19)), 2, 2) // 4 タイルずれ
	apply(t, w, syncMsg(s))
	if you.Actor.X != center(26) {
		t.Fatalf("x = %d, want snapped to %d", you.Actor.X, center(26))
	}
	if _, ok := you.Actor.Target(); !ok {
		t.Fatal("snap must keep the movement intent")
	}
}

func TestSyncDifferentIntentRebuilds(t *testing.T) {
	w := newWorldWithTwo(t)
	you := w.Players["you"] // こちらは停止中
	s := withTarget(idleState("you", center(22), center(19)), 2, 2)
	apply(t, w, syncMsg(s))
	if !you.Actor.Moving() {
		t.Fatal("a missed move must be recovered from Sync")
	}
}

func TestSyncSelfOnlyCorrectsLargeDrift(t *testing.T) {
	w := newWorldWithTwo(t)
	self := w.Self()
	w.SetSelfDir(1, 0)

	// 往復遅延ぶんの小さなずれ（2 タイル）は、予測が正しいとみなして無視する
	apply(t, w, syncMsg(idleState("me", self.Actor.X-2*sim.SubPerTile, self.Actor.Y)))
	if self.Actor.X < center(20) {
		t.Fatal("small drift must not rubber-band the local player")
	}
	if dx, _ := self.Actor.Dir(); dx != 1 {
		t.Fatal("local intent must be kept")
	}

	// 5 タイルのずれは巻き戻す
	apply(t, w, syncMsg(idleState("me", center(10), center(19))))
	if self.Actor.X != center(10) {
		t.Fatalf("x = %d, want snapped to %d", self.Actor.X, center(10))
	}
}

func TestMoveSelf(t *testing.T) {
	w := newWorldWithTwo(t)
	if !w.MoveSelfTo(sim.Point{X: 2, Y: 2}) {
		t.Fatal("reachable target must succeed")
	}
	if w.MoveSelfTo(sim.Point{X: 0, Y: 0}) {
		t.Fatal("a wall must not be a valid target")
	}
	empty := NewWorld(sim.OfficeMap())
	if empty.MoveSelfTo(sim.Point{X: 2, Y: 2}) {
		t.Fatal("must not move before Welcome")
	}
	empty.SetSelfDir(1, 0) // パニックしない
}

func TestErrorMessageBecomesServerError(t *testing.T) {
	w := NewWorld(sim.OfficeMap())
	err := w.Apply(&officev1.ServerMessage{Body: &officev1.ServerMessage_Error{Error: &officev1.Error{
		Code: officev1.ErrorCode_ERROR_CODE_FULL, Message: "room is full",
	}}})
	var se *ServerError
	if !errors.As(err, &se) || se.Code != officev1.ErrorCode_ERROR_CODE_FULL {
		t.Fatalf("err = %v", err)
	}
}

func withTarget(s *officev1.MoveState, x, y int32) *officev1.MoveState {
	s.HasTarget, s.TargetX, s.TargetY = true, x, y
	return s
}

func TestJoinOffline(t *testing.T) {
	w := NewWorld(sim.OfficeMap())
	w.JoinOffline("me", sim.GenderFemale)
	if !w.Ready || w.Self() == nil || w.Self().Gender != sim.GenderFemale || w.Self().Name != "me" {
		t.Fatalf("unexpected offline world: %+v", w.Self())
	}
	if self := w.Self(); self.Actor.Tile() != sim.OfficeSpawn {
		t.Fatalf("must spawn at the office spawn, got %v", self.Actor.Tile())
	}
	if !w.MoveSelfTo(sim.Point{X: 2, Y: 2}) {
		t.Fatal("offline player must be able to move")
	}
}
