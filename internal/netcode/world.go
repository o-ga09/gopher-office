// Package netcode はクライアント側のネットワーク処理（接続、世界の状態、予測と補正）。
// ebiten に依存しないので、bot やテストからも使える。
package netcode

import (
	"fmt"

	officev1 "github.com/o-ga09/gopher-office/gen/office/v1"
	"github.com/o-ga09/gopher-office/internal/sim"
	"github.com/o-ga09/gopher-office/internal/wire"
)

const (
	// otherSnapDist は、他人の位置がサーバとこれ以上ずれたら Sync でスナップする距離（サブユニット）。
	// 他人の再生は受信の遅延と同じだけ遅れるため、通常はずれない。
	otherSnapDist = 1 * sim.SubPerTile
	// selfSnapDist は、自分の位置がサーバとこれ以上ずれたら巻き戻す距離。
	// 自分は予測で先に進むため、往復遅延ぶんは正常なずれになる。それを超える大きなずれだけ補正する。
	selfSnapDist = 3 * sim.SubPerTile
)

// ServerError はサーバが返した Error メッセージ。
type ServerError struct {
	Code    officev1.ErrorCode
	Message string
}

func (e *ServerError) Error() string { return fmt.Sprintf("server error %s: %s", e.Code, e.Message) }

// Player は世界にいる 1 人（自分を含む）。
type Player struct {
	ID       string
	Name     string
	Gender   sim.Gender
	IconHash string
	PID      uint32 // ルーム内の番号（サーバからの Moved / Sync / PlayerLeft はこれで指す）
	Actor    *sim.Actor

	// 直前の tick の位置。描画で tick 間を補間するために使う。
	PrevX, PrevY int
}

// RenderPos は tick 間を補間した描画位置（サブユニット）を返す。alpha は 0〜1。
func (p *Player) RenderPos(alpha float64) (x, y float64) {
	return float64(p.PrevX) + float64(p.Actor.X-p.PrevX)*alpha,
		float64(p.PrevY) + float64(p.Actor.Y-p.PrevY)*alpha
}

// World は、サーバから受け取った情報とローカルの予測から作る、クライアント側の世界の状態。
// 他人は「位置と移動の意図」を受け取って、同じ sim で再生する。自分は入力を先に反映（予測）する。
type World struct {
	Map        *sim.Map
	SelfID     string
	Players    map[string]*Player
	pids       map[uint32]*Player
	Tick       uint32
	MaxPlayers int
	// Ready は Welcome を受け取って、世界に入れたことを表す。
	Ready bool
}

// NewWorld は空の世界を作る。
func NewWorld(m *sim.Map) *World {
	return &World{Map: m, Players: make(map[string]*Player), pids: make(map[uint32]*Player)}
}

// LocalPlayerID はオフライン（サーバなし）のときの自分の ID。
const LocalPlayerID = "local"

// JoinOffline はサーバなしで、スポーン位置に自分だけがいる世界にする。
func (w *World) JoinOffline(name string, gender sim.Gender) {
	a := sim.NewActor(sim.OfficeSpawn)
	w.SelfID = LocalPlayerID
	w.Players = map[string]*Player{LocalPlayerID: {
		ID: LocalPlayerID, Name: name, Gender: gender, Actor: a, PrevX: a.X, PrevY: a.Y,
	}}
	w.Ready = true
}

// Self は自分を返す。Welcome 前は nil。
func (w *World) Self() *Player { return w.Players[w.SelfID] }

func (w *World) newPlayer(p *officev1.Player) *Player {
	a := wire.ActorFromState(w.Map, p.GetState())
	return &Player{
		ID:       p.GetId(),
		PID:      p.GetPid(),
		Name:     p.GetProfile().GetDisplayName(),
		Gender:   wire.GenderFromProto(p.GetProfile().GetGender()),
		IconHash: p.GetProfile().GetIconHash(),
		Actor:    a,
		PrevX:    a.X,
		PrevY:    a.Y,
	}
}

// add はプレイヤーを世界に加える。同じ ID で入り直した古い番号は消す。
func (w *World) add(p *Player) {
	if old := w.Players[p.ID]; old != nil {
		delete(w.pids, old.PID)
	}
	w.Players[p.ID] = p
	w.pids[p.PID] = p
}

// Apply はサーバのメッセージを世界に反映する。Error を受け取った場合は *ServerError を返す。
func (w *World) Apply(msg *officev1.ServerMessage) error {
	switch b := msg.GetBody().(type) {
	case *officev1.ServerMessage_Welcome:
		w.SelfID = b.Welcome.GetPlayerId()
		w.Tick = b.Welcome.GetTick()
		w.MaxPlayers = int(b.Welcome.GetMaxPlayers())
		w.Players = make(map[string]*Player, len(b.Welcome.GetPlayers()))
		w.pids = make(map[uint32]*Player, len(b.Welcome.GetPlayers()))
		for _, p := range b.Welcome.GetPlayers() {
			w.add(w.newPlayer(p))
		}
		w.Ready = w.Self() != nil

	case *officev1.ServerMessage_PlayerJoined:
		if p := b.PlayerJoined.GetPlayer(); p != nil && p.GetId() != "" {
			w.add(w.newPlayer(p))
		}

	case *officev1.ServerMessage_PlayerLeft:
		if p := w.pids[b.PlayerLeft.GetPid()]; p != nil {
			delete(w.Players, p.ID)
			delete(w.pids, p.PID)
		}

	case *officev1.ServerMessage_Moved:
		w.Tick = b.Moved.GetTick()
		for _, s := range b.Moved.GetStates() {
			p := w.pids[s.GetPid()]
			if p == nil || p.ID == w.SelfID {
				continue // 自分の移動はすでに予測で反映している
			}
			// 意図が変わった瞬間の状態なので、同じ位置・同じ意図から作れば、サーバと同じ軌跡を再生できる
			p.Actor = wire.ActorFromState(w.Map, s)
			p.PrevX, p.PrevY = p.Actor.X, p.Actor.Y
		}

	case *officev1.ServerMessage_Sync:
		w.Tick = b.Sync.GetTick()
		for _, s := range b.Sync.GetStates() {
			w.correct(s)
		}

	case *officev1.ServerMessage_Error:
		return &ServerError{Code: b.Error.GetCode(), Message: b.Error.GetMessage()}
	}
	return nil
}

// correct は Sync の状態を使って、ドリフトを補正する。
func (w *World) correct(s *officev1.MoveState) {
	p := w.pids[s.GetPid()]
	if p == nil {
		return
	}
	drift := max(abs(p.Actor.X-int(s.GetX())), abs(p.Actor.Y-int(s.GetY())))

	if p.ID == w.SelfID {
		// 自分の意図はこちらが最新（サーバの状態は往復遅延ぶん古い）なので、大きくずれたときだけ位置を戻す
		if drift > selfSnapDist {
			p.Actor.Teleport(int(s.GetX()), int(s.GetY()))
			p.PrevX, p.PrevY = p.Actor.X, p.Actor.Y
		}
		return
	}

	// 途中の状態から作り直すと別の経路になるため、意図が同じなら経路は保ち、位置だけ補正する
	if wire.SameIntent(p.Actor, s) {
		if drift > otherSnapDist {
			p.Actor.Teleport(int(s.GetX()), int(s.GetY()))
			p.PrevX, p.PrevY = p.Actor.X, p.Actor.Y
		}
		return
	}
	p.Actor = wire.ActorFromState(w.Map, s)
	p.PrevX, p.PrevY = p.Actor.X, p.Actor.Y
}

// Step は世界を 1 tick（20Hz）進める。
func (w *World) Step() {
	for _, p := range w.Players {
		p.PrevX, p.PrevY = p.Actor.X, p.Actor.Y
		p.Actor.Step(w.Map)
	}
}

// MoveSelfTo は自分をクリック移動させる。サーバへ送るべきなら true。
func (w *World) MoveSelfTo(t sim.Point) bool {
	self := w.Self()
	return self != nil && self.Actor.MoveTo(w.Map, t)
}

// SetSelfDir は自分の方向入力を設定する。
func (w *World) SetSelfDir(dx, dy int) {
	if self := w.Self(); self != nil {
		self.Actor.SetDir(dx, dy)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
