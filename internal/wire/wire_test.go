package wire

import (
	"testing"

	officev1 "github.com/o-ga09/gopher-office/gen/office/v1"
	"github.com/o-ga09/gopher-office/internal/sim"
)

func TestGenderRoundTrip(t *testing.T) {
	for _, g := range []sim.Gender{sim.GenderMale, sim.GenderFemale, sim.GenderOther} {
		if got := GenderFromProto(GenderToProto(g)); got != g {
			t.Errorf("round trip %v -> %v", g, got)
		}
	}
	if got := GenderFromProto(officev1.Gender(99)); got != sim.GenderOther {
		t.Errorf("unknown gender = %v, want other", got)
	}
}

// サーバがコマンドを適用した直後（Step の前）の状態から ActorFromState で再現した Actor は、
// 元の Actor と同じ軌跡をたどる（クライアントが他人の移動を再生できる前提）。
// 移動の途中で新しい目的地を指定した場合も同様。
func TestStateRoundTripReplaysSameTrajectory(t *testing.T) {
	m := sim.OfficeMap()

	server := sim.NewActor(sim.OfficeSpawn)
	if !server.MoveTo(m, sim.Point{X: 2, Y: 2}) {
		t.Fatal("MoveTo failed")
	}
	for i := 0; i < 10; i++ {
		server.Step(m)
	}
	// 移動の途中で行き先を変える。その直後の状態を送る
	if !server.MoveTo(m, sim.Point{X: 30, Y: 3}) {
		t.Fatal("second MoveTo failed")
	}

	client := ActorFromState(m, StateOf("p1", server))
	for i := 0; i < 200; i++ {
		server.Step(m)
		client.Step(m)
		if server.X != client.X || server.Y != client.Y {
			t.Fatalf("tick %d: server (%d,%d) != client (%d,%d)", i, server.X, server.Y, client.X, client.Y)
		}
	}
}

func TestStateOfDir(t *testing.T) {
	a := sim.NewActor(sim.OfficeSpawn)
	a.SetDir(1, 0)
	s := StateOf("p1", a)
	if s.GetPlayerId() != "p1" || s.GetDirX() != 1 || s.GetHasTarget() {
		t.Fatalf("unexpected state: %v", s)
	}
	b := ActorFromState(sim.OfficeMap(), s)
	if dx, dy := b.Dir(); dx != 1 || dy != 0 {
		t.Fatalf("dir = (%d,%d)", dx, dy)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	in := &officev1.ServerMessage{Body: &officev1.ServerMessage_Moved{Moved: &officev1.Moved{
		Tick:   7,
		States: []*officev1.MoveState{{PlayerId: "a", X: 1, Y: 2}},
	}}}
	b, err := Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := UnmarshalServer(b)
	if err != nil {
		t.Fatal(err)
	}
	if out.GetMoved().GetTick() != 7 || out.GetMoved().GetStates()[0].GetPlayerId() != "a" {
		t.Fatalf("round trip mismatch: %v", out)
	}
	if _, err := UnmarshalClient([]byte{0xff, 0xff, 0xff}); err == nil {
		t.Fatal("garbage must fail to decode")
	}
}

func TestSameIntent(t *testing.T) {
	m := sim.OfficeMap()
	a := sim.NewActor(sim.OfficeSpawn)
	a.MoveTo(m, sim.Point{X: 2, Y: 2})
	s := StateOf("p1", a)
	if !SameIntent(a, s) {
		t.Fatal("same actor must have the same intent")
	}
	for i := 0; i < 5; i++ { // 移動が進んでも、意図（目的地）は同じ
		a.Step(m)
	}
	if !SameIntent(a, s) {
		t.Fatal("progress along the path must not change the intent")
	}
	b := sim.NewActor(sim.OfficeSpawn)
	b.MoveTo(m, sim.Point{X: 3, Y: 2})
	if SameIntent(b, s) {
		t.Fatal("different target must differ")
	}
	c := sim.NewActor(sim.OfficeSpawn)
	c.SetDir(1, 0)
	if SameIntent(c, s) {
		t.Fatal("dir vs target must differ")
	}
}
