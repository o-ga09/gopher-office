// Package gameserver はルーム 1 つ分のリアルタイムサーバ（Agones の GameServer の中身）。
//
// ルームの状態は Run の goroutine だけが持つ。接続ごとの goroutine はコマンドをチャネルで渡すだけで、
// 状態には触れない（ロック競合を避ける）。
package gameserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	officev1 "github.com/o-ga09/gopher-office/gen/office/v1"
	"github.com/o-ga09/gopher-office/internal/sim"
	"github.com/o-ga09/gopher-office/internal/wire"
)

const (
	// syncEvery は全員の状態を配る間隔（tick）。1Hz。
	syncEvery = sim.TickRate
	// syncPlayersPerSecond は、Sync の間隔を 1 秒延ばす人数の単位（syncInterval 参照）。
	syncPlayersPerSecond = 100
	// outBuffer は接続ごとの送信キューの長さ。
	outBuffer = 256
	// cmdBuffer はルームへのコマンドキューの長さ。
	cmdBuffer = 1024
	// maxSignalPayload は通話のシグナリング 1 通の上限（バイト）。音声のみの SDP は 2KB 前後。
	maxSignalPayload = 6000
)

// Config はルームの設定。
type Config struct {
	RoomID      string
	MapID       string
	Map         *sim.Map
	MaxPlayers  int
	IdleTimeout time.Duration // 無人がこの時間続いたら OnIdle を呼ぶ。0 なら呼ばない
	OnIdle      func()
	// DeferIdle を true にすると、Arm が呼ばれるまで無人の時間を数えない。
	// Agones では、待機中（Ready）の GameServer を止めないよう、割り当てられてから数え始める。
	DeferIdle bool
	Verifier  TokenVerifier
	Logger    *slog.Logger
	// OriginPatterns は WebSocket で許可する Origin（host のパターン）。空なら同一オリジンのみ。
	OriginPatterns []string
}

// conn はルームから見た 1 接続。actor / dirty / kicked は Run の goroutine だけが触る。
type conn struct {
	id  string
	pid uint32 // ルーム内の番号（入室時に決まる）
	// idleSynced は、止まっている状態を Sync で送り済みか。動き出すと false に戻る
	idleSynced bool
	profile    *officev1.Profile
	actor      *sim.Actor
	dirty      bool // 今 tick で移動の意図が変わった
	kicked     bool // 追い出し済み（以後は送らない）

	out  chan []byte
	done chan struct{}

	closeOnce sync.Once
	closeWS   func(reason string)
}

func newConn(id string, p *officev1.Profile) *conn {
	return &conn{id: id, profile: p, out: make(chan []byte, outBuffer), done: make(chan struct{})}
}

// shutdown は接続を閉じる（複数回呼んでよい）。
func (c *conn) shutdown(reason string) {
	c.closeOnce.Do(func() {
		close(c.done)
		if c.closeWS != nil {
			go c.closeWS(reason)
		}
	})
}

type (
	joinCmd struct {
		c     *conn
		reply chan joinResult
	}
	leaveCmd struct{ c *conn }
	inputCmd struct {
		c   *conn
		msg *officev1.ClientMessage
	}
	// signalCmd は通話のシグナリングの中継依頼。
	signalCmd struct {
		from    *conn
		to      uint32
		payload []byte
	}
)

type joinResult struct{ full bool }

// Room は 1 つのルーム。
type Room struct {
	cfg Config
	log *slog.Logger

	cmds    chan any
	stopped chan struct{}

	players    map[string]*conn
	joinCount  int
	nextPID    uint32 // 入室のたびに増やす。ルームが続く間は再利用しない
	walkable   []sim.Point
	tick       uint32
	emptySince time.Time
	idleFired  bool

	m       metrics
	armedAt atomic.Int64 // Arm を呼んだ時刻（UnixNano）。0 なら未了

	serverTick atomic.Uint32
	numPlayers atomic.Int32
}

// NewRoom はルームを作る。Run を呼ぶまで動かない。
func NewRoom(cfg Config) *Room {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Map == nil {
		cfg.Map = sim.OfficeMap()
	}
	if cfg.MapID == "" {
		cfg.MapID = "office-1"
	}
	if cfg.MaxPlayers <= 0 {
		cfg.MaxPlayers = 200
	}
	return &Room{
		cfg:      cfg,
		log:      cfg.Logger.With("room", cfg.RoomID),
		cmds:     make(chan any, cmdBuffer),
		stopped:  make(chan struct{}),
		players:  make(map[string]*conn),
		walkable: cfg.Map.WalkableTiles(),
	}
}

// Arm は、無人の時間を数え始めてよいことを知らせる（Config.DeferIdle のとき）。何度呼んでもよい。
func (r *Room) Arm() { r.armedAt.CompareAndSwap(0, time.Now().UnixNano()) }

// Players は現在の人数を返す。
func (r *Room) Players() int { return int(r.numPlayers.Load()) }

// Run はシミュレーションループ。ctx が終わるまで戻らない。
func (r *Room) Run(ctx context.Context) {
	defer close(r.stopped)
	ticker := time.NewTicker(time.Second / sim.TickRate)
	defer ticker.Stop()
	r.emptySince = time.Now()

	for {
		select {
		case <-ctx.Done():
			for _, c := range r.players {
				c.shutdown("server shutting down")
			}
			return
		case cmd := <-r.cmds:
			r.handle(cmd)
		case now := <-ticker.C:
			r.step(now)
			r.m.observeTick(time.Since(now), time.Second/sim.TickRate)
		}
	}
}

// submit はコマンドをルームに渡す。ルームが止まっている場合は false。
func (r *Room) submit(ctx context.Context, cmd any) bool {
	select {
	case r.cmds <- cmd:
		return true
	case <-r.stopped:
		return false
	case <-ctx.Done():
		return false
	}
}

func (r *Room) handle(cmd any) {
	switch cmd := cmd.(type) {
	case joinCmd:
		cmd.reply <- r.join(cmd.c)
	case leaveCmd:
		if r.players[cmd.c.id] == cmd.c { // 同じ ID で入り直された古い接続の退室は無視する
			r.remove(cmd.c)
		}
	case inputCmd:
		if r.players[cmd.c.id] == cmd.c {
			r.input(cmd.c, cmd.msg)
		}
	case signalCmd:
		if r.players[cmd.from.id] == cmd.from {
			r.relaySignal(cmd)
		}
	}
}

func (r *Room) join(c *conn) joinResult {
	// 同じ ID で再接続した場合は、古い接続を追い出して入れ替える
	if old := r.players[c.id]; old != nil {
		r.remove(old)
		old.shutdown("replaced by a new connection")
	}
	if len(r.players) >= r.cfg.MaxPlayers {
		return joinResult{full: true}
	}

	c.actor = sim.NewActor(r.spawnTile())
	r.joinCount++
	r.nextPID++
	c.pid = r.nextPID
	r.players[c.id] = c
	r.numPlayers.Store(int32(len(r.players))) //nolint:gosec // 人数は上限（数百）で int32 に収まる

	r.send(c, r.encode(&officev1.ServerMessage{Body: &officev1.ServerMessage_Welcome{Welcome: r.welcome(c)}}), false)
	joined := r.encode(&officev1.ServerMessage{Body: &officev1.ServerMessage_PlayerJoined{
		PlayerJoined: &officev1.PlayerJoined{Player: r.playerOf(c)},
	}})
	for _, o := range r.players {
		if o != c {
			r.send(o, joined, false)
		}
	}
	r.log.Info("player joined", "player", c.id, "name", c.profile.GetDisplayName(), "players", len(r.players))
	return joinResult{}
}

func (r *Room) remove(c *conn) {
	delete(r.players, c.id)
	r.numPlayers.Store(int32(len(r.players))) //nolint:gosec // 同上
	if len(r.players) == 0 {
		r.emptySince = time.Now()
	}
	left := r.encode(&officev1.ServerMessage{Body: &officev1.ServerMessage_PlayerLeft{
		PlayerLeft: &officev1.PlayerLeft{Pid: c.pid},
	}})
	for _, o := range r.players {
		r.send(o, left, false)
	}
	r.log.Info("player left", "player", c.id, "players", len(r.players))
}

func (r *Room) input(c *conn, msg *officev1.ClientMessage) {
	switch b := msg.GetBody().(type) {
	case *officev1.ClientMessage_MoveTo:
		p := sim.Point{X: int(b.MoveTo.GetX()), Y: int(b.MoveTo.GetY())}
		if r.cfg.Map.In(p.X, p.Y) && c.actor.MoveTo(r.cfg.Map, p) {
			c.dirty = true
		}
	case *officev1.ClientMessage_MoveDir:
		dx, dy := int(b.MoveDir.GetDx()), int(b.MoveDir.GetDy())
		if curX, curY := c.actor.Dir(); dx == 0 && dy == 0 && curX == 0 && curY == 0 {
			return // 止まっているのに「止まれ」は変化なし
		}
		c.actor.SetDir(dx, dy)
		c.dirty = true
	}
}

// relaySignal は通話のシグナリングを宛先へそのまま届ける。宛先がいない・自分宛て・混んでいるときは捨てる
// （WebRTC 側がタイムアウトして張り直す。シグナリングで遅い接続を追い出さない）。
func (r *Room) relaySignal(cmd signalCmd) {
	if cmd.to == cmd.from.pid {
		return
	}
	for _, o := range r.players {
		if o.pid != cmd.to {
			continue
		}
		b := r.encode(&officev1.ServerMessage{Body: &officev1.ServerMessage_Signal{
			Signal: &officev1.Signal{Pid: cmd.from.pid, Payload: cmd.payload},
		}})
		r.send(o, b, true)
		return
	}
}

// step は 1 tick 進める。
func (r *Room) step(now time.Time) {
	r.tick++
	r.serverTick.Store(r.tick)

	// 意図が変わった人の状態を、Step の前に配る。クライアントはその状態から同じ軌跡を再生できる
	var moved []*officev1.MoveState
	for _, c := range r.players {
		if c.dirty {
			moved = append(moved, wire.StateOf(c.pid, c.actor))
			c.dirty = false
		}
	}
	if len(moved) > 0 {
		b := r.encode(&officev1.ServerMessage{Body: &officev1.ServerMessage_Moved{Moved: &officev1.Moved{Tick: r.tick, States: moved}}})
		for _, c := range r.players {
			r.send(c, b, false)
		}
	}

	for _, c := range r.players {
		c.actor.Step(r.cfg.Map)
	}

	// ドリフト補正。遅い接続では捨ててよい
	if len(r.players) > 0 && r.tick%syncInterval(len(r.players)) == 0 {
		// 止まっている人は、止まった直後に 1 回だけ送る。以後は位置が変わらず、クライアントも知っているので省く
		// （人数が多いほど、帯域のほとんどが Sync になるため）
		states := make([]*officev1.MoveState, 0, len(r.players))
		for _, c := range r.players {
			if moving(c.actor) {
				c.idleSynced = false
			} else if c.idleSynced {
				continue
			} else {
				c.idleSynced = true
			}
			states = append(states, wire.StateOf(c.pid, c.actor))
		}
		if len(states) > 0 {
			b := r.encode(&officev1.ServerMessage{Body: &officev1.ServerMessage_Sync{Sync: &officev1.Sync{Tick: r.tick, States: states}}})
			for _, c := range r.players {
				r.send(c, b, true)
			}
		}
	}

	since, counting := r.emptySince, true
	if r.cfg.DeferIdle {
		a := r.armedAt.Load()
		counting = a != 0
		if t := time.Unix(0, a); t.After(since) {
			since = t // 割り当てられた時点から数える
		}
	}
	if counting && len(r.players) == 0 && r.cfg.IdleTimeout > 0 && !r.idleFired && now.Sub(since) >= r.cfg.IdleTimeout {
		r.idleFired = true
		r.log.Info("room is idle", "idle_timeout", r.cfg.IdleTimeout)
		if r.cfg.OnIdle != nil {
			r.cfg.OnIdle()
		}
	}
}

// send は接続の送信キューに積む。キューが溢れたとき、droppable なら捨て、そうでなければ追い出す。
func (r *Room) send(c *conn, b []byte, droppable bool) {
	if c.kicked {
		return
	}
	select {
	case c.out <- b:
	default:
		if droppable {
			r.m.dropped.Add(1)
			return
		}
		r.m.kicked.Add(1)
		c.kicked = true
		r.log.Warn("kick slow consumer", "player", c.id)
		c.shutdown("slow consumer")
	}
}

func (r *Room) encode(m *officev1.ServerMessage) []byte {
	b, err := wire.Marshal(m)
	if err != nil {
		r.log.Error("marshal failed", "err", err) // proto の符号化は通常失敗しない
		return nil
	}
	return b
}

func (r *Room) playerOf(c *conn) *officev1.Player {
	return &officev1.Player{Id: c.id, Pid: c.pid, Profile: c.profile, State: wire.StateOf(c.pid, c.actor)}
}

func (r *Room) welcome(c *conn) *officev1.Welcome {
	players := make([]*officev1.Player, 0, len(r.players))
	for _, o := range r.players {
		players = append(players, r.playerOf(o))
	}
	return &officev1.Welcome{
		PlayerId:   c.id,
		SelfPid:    c.pid,
		Tick:       r.tick,
		TickRate:   sim.TickRate,
		MapId:      r.cfg.MapID,
		MaxPlayers: uint32(r.cfg.MaxPlayers), //nolint:gosec // 設定値（正の小さい数）
		Players:    players,
	}
}

// spawnTile は入室位置を決める。最初の 1 人は決まった位置、以後は歩ける場所に散らす
// （大勢が入っても重ならないように。乱数は使わず決定的にする）。
func (r *Room) spawnTile() sim.Point {
	if r.joinCount == 0 {
		return sim.OfficeSpawn
	}
	return r.walkable[(r.joinCount*7919)%len(r.walkable)]
}

// newPlayerID はランダムなプレイヤー ID を作る（トークンに ID が無いローカル用）。
func newPlayerID() string {
	var b [8]byte
	_, _ = rand.Read(b[:]) // crypto/rand は失敗しない（Go 1.24 以降）
	return hex.EncodeToString(b[:])
}

// moving は、方向入力またはクリック移動の目的地を持っている（動いている途中の）ときに true。
func moving(a *sim.Actor) bool {
	if dx, dy := a.Dir(); dx != 0 || dy != 0 {
		return true
	}
	_, ok := a.Target()
	return ok
}

// syncInterval は Sync を送る間隔（tick）。100 人までは 1 秒ごと、以後は 100 人増えるごとに 1 秒ずつ延ばす。
// Sync は、決定的な再生のずれを直すための保険で、ずれは普通は起きない。人数が増えるほど 1 回が大きくなり、
// 送る相手も増える（帯域は人数の 2 乗に比例する）ため、間引いて帯域の伸びを抑える。
func syncInterval(players int) uint32 {
	return syncEvery * uint32((players+syncPlayersPerSecond-1)/syncPlayersPerSecond) //nolint:gosec // 人数は上限（数百）で小さい
}
