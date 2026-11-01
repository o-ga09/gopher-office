package voice

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestGain(t *testing.T) {
	for _, tc := range []struct{ d, want float64 }{
		{0, 1}, {FullVolumeRadius, 1}, {HearRadius, 0}, {HearRadius + 5, 0},
		{(FullVolumeRadius + HearRadius) / 2, 0.5},
	} {
		if got := Gain(tc.d); got != tc.want {
			t.Errorf("Gain(%v) = %v, want %v", tc.d, got, tc.want)
		}
	}
}

func TestPlanConnectsNearestUpToTheLimit(t *testing.T) {
	p := newPlanner()
	var ns []Neighbor
	for i := range 8 {
		ns = append(ns, Neighbor{PID: uint32(10 + i), Dist: float64(i) + 1}) //nolint:gosec // 小さい値
	}
	ns = append(ns, Neighbor{PID: 99, Dist: HearRadius + 1}) // 遠い人はつながない
	got := p.plan(ns, nil)
	if want := []uint32{10, 11, 12, 13, 14}; !reflect.DeepEqual(got.Connect, want) {
		t.Fatalf("connect = %v, want %v", got.Connect, want)
	}
	if len(got.Disconnect) != 0 {
		t.Fatalf("disconnect = %v", got.Disconnect)
	}
}

func TestPlanKeepsConnectionsInsideDropRadius(t *testing.T) {
	p := newPlanner()
	p.plan([]Neighbor{{PID: 1, Dist: 3}}, nil)
	// HearRadius を少し越えても、DropRadius までは切らない（境目で出入りしない）
	got := p.plan([]Neighbor{{PID: 1, Dist: HearRadius + 1}}, nil)
	if len(got.Connect)+len(got.Disconnect) != 0 || got.Gains[1] != 0 {
		t.Fatalf("plan = %+v", got)
	}
	got = p.plan([]Neighbor{{PID: 1, Dist: DropRadius + 1}}, nil)
	if !reflect.DeepEqual(got.Disconnect, []uint32{1}) {
		t.Fatalf("disconnect = %v", got.Disconnect)
	}
	// 退室した人（一覧にいない）も切る
	p.plan([]Neighbor{{PID: 2, Dist: 1}}, nil)
	got = p.plan(nil, nil)
	if !reflect.DeepEqual(got.Disconnect, []uint32{2}) {
		t.Fatalf("disconnect on leave = %v", got.Disconnect)
	}
}

func TestPlanDoesNotEvictExistingForCloserNewcomer(t *testing.T) {
	p := newPlanner()
	var ns []Neighbor
	for i := range MaxPeers {
		ns = append(ns, Neighbor{PID: uint32(i + 1), Dist: 6}) //nolint:gosec // 小さい値
	}
	p.plan(ns, nil)
	got := p.plan(append(ns, Neighbor{PID: 50, Dist: 1}), nil)
	if len(got.Connect) != 0 || len(got.Disconnect) != 0 {
		t.Fatalf("existing peers must stay: %+v", got)
	}
}

type fakeEngine struct {
	h        Handler
	startErr error
	started  func(error)
	calls    []string
	gains    map[uint32]float64
}

func (f *fakeEngine) SetHandler(h Handler) { f.h = h }
func (f *fakeEngine) Start(done func(error)) {
	f.calls = append(f.calls, "start")
	f.started = done
	if f.startErr != nil || done == nil {
		return
	}
}
func (f *fakeEngine) Stop() { f.calls = append(f.calls, "stop") }
func (f *fakeEngine) SetMuted(m bool) {
	f.calls = append(f.calls, map[bool]string{true: "mute", false: "unmute"}[m])
}
func (f *fakeEngine) Disconnect(pid uint32)       { f.calls = append(f.calls, "disconnect", itoa(pid)) }
func (f *fakeEngine) Signal(pid uint32, _ []byte) { f.calls = append(f.calls, "signal", itoa(pid)) }
func (f *fakeEngine) Connect(pid uint32, init bool) {
	f.calls = append(f.calls, "connect", itoa(pid), map[bool]string{true: "offer", false: "answer"}[init])
}
func (f *fakeEngine) SetGain(pid uint32, g float64) {
	if f.gains == nil {
		f.gains = map[uint32]float64{}
	}
	f.gains[pid] = g
}

func itoa(v uint32) string { return string(rune('0' + v)) }

func newOn(t *testing.T) (*Controller, *fakeEngine, *[][2]any) {
	t.Helper()
	f := &fakeEngine{}
	var sent [][2]any
	c := New(f, func(to uint32, p []byte) { sent = append(sent, [2]any{to, string(p)}) })
	c.SetSelf(2)
	c.Toggle()
	f.started(nil)
	f.calls = nil
	return c, f, &sent
}

func TestControllerStartsOnlyOnToggleAndReportsFailure(t *testing.T) {
	f := &fakeEngine{}
	c := New(f, nil)
	c.Update([]Neighbor{{PID: 3, Dist: 1}})
	if len(f.calls) != 0 {
		t.Fatalf("must not touch the engine before Toggle: %v", f.calls)
	}
	c.Toggle()
	if s, _ := c.State(); s != Starting {
		t.Fatalf("state = %v", s)
	}
	f.started(errors.New("denied"))
	if s, err := c.State(); s != Failed || err == nil {
		t.Fatalf("state = %v, %v", s, err)
	}
	c.Toggle() // 失敗後はやり直せる
	f.started(nil)
	if s, _ := c.State(); s != On {
		t.Fatalf("state = %v", s)
	}
}

func TestControllerOfferSideIsTheSmallerPID(t *testing.T) {
	c, f, _ := newOn(t) // self = 2
	c.Update([]Neighbor{{PID: 1, Dist: 1}, {PID: 3, Dist: 1}})
	want := []string{"connect", "1", "answer", "connect", "3", "offer"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls = %v, want %v", f.calls, want)
	}
	if f.gains[1] != 1 {
		t.Fatalf("gain = %v", f.gains[1])
	}
}

func TestControllerSignalRouting(t *testing.T) {
	c, f, sent := newOn(t)
	c.HandleSignal(5, []byte("x")) // 近くにいない人からは受けない
	if len(f.calls) != 0 {
		t.Fatalf("calls = %v", f.calls)
	}
	c.Update([]Neighbor{{PID: 1, Dist: 9}}) // つなぐ前（HearRadius の外）
	c.HandleSignal(1, []byte("offer"))      // 近くにいるので先回りの offer は受ける
	if !reflect.DeepEqual(f.calls, []string{"signal", "1"}) {
		t.Fatalf("calls = %v", f.calls)
	}
	f.h.OnSignal(1, []byte("answer"))
	if !reflect.DeepEqual(*sent, [][2]any{{uint32(1), "answer"}}) {
		t.Fatalf("sent = %v", *sent)
	}
}

func TestControllerRetriesClosedPeerAfterCooldown(t *testing.T) {
	c, f, _ := newOn(t)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	ns := []Neighbor{{PID: 3, Dist: 1}}
	c.Update(ns)
	f.h.OnPeerClosed(3)
	f.calls = nil
	c.Update(ns)
	if len(f.calls) != 0 {
		t.Fatalf("must wait before retrying: %v", f.calls)
	}
	now = now.Add(retryAfter + time.Second)
	c.Update(ns)
	if len(f.calls) == 0 || f.calls[0] != "connect" {
		t.Fatalf("must retry: %v", f.calls)
	}
}

func TestControllerStopAndMute(t *testing.T) {
	c, f, _ := newOn(t)
	c.ToggleMute()
	c.Update([]Neighbor{{PID: 3, Dist: 1}})
	c.Toggle() // 終了
	if s, _ := c.State(); s != Off || c.Peers() != 0 {
		t.Fatalf("state = %v peers = %d", s, c.Peers())
	}
	want := []string{"mute", "connect", "3", "offer", "stop"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls = %v, want %v", f.calls, want)
	}
}
