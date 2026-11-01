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
	// outBuffer は接続ごとの送信キューの長さ。
	outBuffer = 256
	// cmdBuffer はルームへのコマンドキューの長さ。
	cmdBuffer = 1024
)

// Config はルームの設定。
type Config struct {
	RoomID      string
	MapID       string
	Map         *sim.Map
	MaxPlayers  int
	IdleTimeout time.Duration // 無人がこの時間続いたら OnIdle を呼ぶ。0 なら呼ばない
	OnIdle      func()
	Verifier    TokenVerifier
	Logger      *slog.Logger
	// OriginPatterns は WebSocket で許可する Origin（host のパターン）。空なら同一オリジンのみ。
	OriginPatterns []string
}

// conn はルームから見た 1 接続。actor / dirty / kicked は Run の goroutine だけが触る。
type conn struct {
	id      string
	profile *officev1.Profile
	actor   *sim.Actor
	dirty   bool // 今 tick で移動の意図が変わった
	kicked  bool // 追い出し済み（以後は送らない）

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
	walkable   []sim.Point
	tick       uint32
	emptySince time.Time
	idleFired  bool

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
		PlayerLeft: &officev1.PlayerLeft{PlayerId: c.id},
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

// step は 1 tick 進める。
func (r *Room) step(now time.Time) {
	r.tick++
	r.serverTick.Store(r.tick)

	// 意図が変わった人の状態を、Step の前に配る。クライアントはその状態から同じ軌跡を再生できる
	var moved []*officev1.MoveState
	for _, c := range r.players {
		if c.dirty {
			moved = append(moved, wire.StateOf(c.id, c.actor))
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
	if r.tick%syncEvery == 0 && len(r.players) > 0 {
		states := make([]*officev1.MoveState, 0, len(r.players))
		for _, c := range r.players {
			states = append(states, wire.StateOf(c.id, c.actor))
		}
		b := r.encode(&officev1.ServerMessage{Body: &officev1.ServerMessage_Sync{Sync: &officev1.Sync{Tick: r.tick, States: states}}})
		for _, c := range r.players {
			r.send(c, b, true)
		}
	}

	if len(r.players) == 0 && r.cfg.IdleTimeout > 0 && !r.idleFired && now.Sub(r.emptySince) >= r.cfg.IdleTimeout {
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
			return
		}
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
	return &officev1.Player{Id: c.id, Profile: c.profile, State: wire.StateOf(c.id, c.actor)}
}

func (r *Room) welcome(c *conn) *officev1.Welcome {
	players := make([]*officev1.Player, 0, len(r.players))
	for _, o := range r.players {
		players = append(players, r.playerOf(o))
	}
	return &officev1.Welcome{
		PlayerId:   c.id,
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
