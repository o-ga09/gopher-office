package voice

import (
	"sync"
	"time"
)

// retryAfter は、つなげなかった（切れた）相手に、もう一度つなごうとするまでの待ち時間。
const retryAfter = 5 * time.Second

// Engine は WebRTC の実装（ブラウザ）。Controller の呼び出しは、すべてゲームループの goroutine から行う。
type Engine interface {
	// Start はマイクを使い始める。許可を待つので、結果は done で返す（別の goroutine から呼んでよい）。
	Start(done func(error))
	Stop()
	// Connect は相手とのつなぎ方を始める。initiator が true なら offer を作る側。
	Connect(pid uint32, initiator bool)
	Disconnect(pid uint32)
	// Signal は相手から届いたシグナリングを渡す。
	Signal(pid uint32, payload []byte)
	SetGain(pid uint32, gain float64)
	SetMuted(muted bool)
	// SetCamera はカメラを入れる／切る。入れるときは許可を待つので、結果は done で返す（別の goroutine から呼んでよい）。
	SetCamera(on bool, done func(error))
	// SetLabel は相手の映像の枠に出す名前を設定する。
	SetLabel(pid uint32, name string)
	// SetHandler は Engine からの通知の宛先を設定する（Start の前に呼ぶ）。
	SetHandler(h Handler)
}

// Handler は Engine から Controller への通知。どの goroutine からも呼ばれうる。
type Handler interface {
	// OnSignal は相手へ送るシグナリングができたときに呼ばれる。
	OnSignal(to uint32, payload []byte)
	// OnPeerClosed は相手との接続が切れた（失敗した）ときに呼ばれる。
	OnPeerClosed(pid uint32)
}

// State は通話の状態。
type State int

const (
	Off      State = iota // 使っていない（初期状態。マイクは要求しない）
	Starting              // マイクの許可を待っている
	On                    // 通話中
	Failed                // マイクを使えなかった（Err に理由）
)

// Controller は、近くにいる人と自動でつなぎ、距離に応じて音量を変える。
// 初期状態は Off で、利用者が Toggle したときだけマイクを使い始める。
type Controller struct {
	eng  Engine
	send func(to uint32, payload []byte) // シグナリングをサーバへ送る

	mu        sync.Mutex
	state     State
	err       error
	muted     bool
	camera    bool   // カメラが入っている
	cameraErr error  // 直近のカメラの失敗
	self      uint32 // 自分の pid（offer を作る側を決めるのに使う）
	planner   *planner
	neighbors map[uint32]float64 // 直近の Update で渡された近くの人（pid → 距離）
	cooldown  map[uint32]time.Time
	now       func() time.Time
}

// New は Controller を作る。send は、サーバへシグナリングを送る関数。
func New(eng Engine, send func(to uint32, payload []byte)) *Controller {
	c := &Controller{
		eng: eng, send: send, planner: newPlanner(),
		neighbors: map[uint32]float64{}, cooldown: map[uint32]time.Time{}, now: time.Now,
	}
	eng.SetHandler(c)
	return c
}

// SetSelf は自分の pid を設定する（Welcome を受け取ったあとに呼ぶ）。
func (c *Controller) SetSelf(pid uint32) {
	c.mu.Lock()
	c.self = pid
	c.mu.Unlock()
}

// State は今の状態と、Failed のときの理由。
func (c *Controller) State() (State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state, c.err
}

// Muted はマイクをミュートしているか。
func (c *Controller) Muted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.muted
}

// Peers は今つないでいる（つなごうとしている）相手の数。
func (c *Controller) Peers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.planner.connected)
}

// Toggle は通話を始める／終える。始めるときだけマイクの許可を求める。
func (c *Controller) Toggle() {
	c.mu.Lock()
	switch c.state {
	case Starting:
		c.mu.Unlock()
		return
	case On:
		c.stopLocked()
		c.mu.Unlock()
		return
	}
	c.state, c.err = Starting, nil
	c.mu.Unlock()

	c.eng.Start(func(err error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.state != Starting {
			return
		}
		if err != nil {
			c.state, c.err = Failed, err
			return
		}
		c.state = On
		c.eng.SetMuted(c.muted)
	})
}

// Camera はカメラが入っているか。失敗したときは理由も返す。
func (c *Controller) Camera() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.camera, c.cameraErr
}

// ToggleCamera はカメラを入れる／切る。通話中だけ使える。映像は音声と同じ相手にだけ届く。
func (c *Controller) ToggleCamera() {
	c.mu.Lock()
	if c.state != On {
		c.mu.Unlock()
		return
	}
	if c.camera {
		c.camera = false
		c.mu.Unlock()
		c.eng.SetCamera(false, nil)
		return
	}
	c.cameraErr = nil
	c.mu.Unlock()
	c.eng.SetCamera(true, func(err error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.state != On {
			return
		}
		c.camera, c.cameraErr = err == nil, err
	})
}

// ToggleMute はマイクのミュートを切り替える。
func (c *Controller) ToggleMute() {
	c.mu.Lock()
	c.muted = !c.muted
	m, on := c.muted, c.state == On
	c.mu.Unlock()
	if on {
		c.eng.SetMuted(m)
	}
}

// Close は通話を終える（退室・切断のとき）。
func (c *Controller) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == On || c.state == Starting {
		c.stopLocked()
	}
}

func (c *Controller) stopLocked() {
	c.eng.Stop()
	c.camera = false
	c.state = Off
	c.planner = newPlanner()
	c.neighbors = map[uint32]float64{}
}

// Update は近くにいる人の一覧（自分以外）から、つなぐ相手と音量を更新する。0.5 秒ごとなど、ゆっくりした間隔で呼ぶ。
func (c *Controller) Update(ns []Neighbor) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != On {
		return
	}
	c.neighbors = make(map[uint32]float64, len(ns))
	names := make(map[uint32]string, len(ns))
	for _, n := range ns {
		c.neighbors[n.PID] = n.Dist
		names[n.PID] = n.Name
	}
	now := c.now()
	plan := c.planner.plan(ns, func(pid uint32) bool { return now.Before(c.cooldown[pid]) })
	for _, pid := range plan.Disconnect {
		c.eng.Disconnect(pid)
	}
	for _, pid := range plan.Connect {
		// 両方が同時に offer を出して衝突しないよう、pid の小さい側が offer を作る
		c.eng.Connect(pid, c.self < pid)
	}
	for pid, g := range plan.Gains {
		c.eng.SetLabel(pid, names[pid])
		c.eng.SetGain(pid, g)
	}
}

// HandleSignal はサーバから届いたシグナリングを渡す。近くにいない人からのものは捨てる。
func (c *Controller) HandleSignal(from uint32, payload []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != On {
		return
	}
	if !c.planner.connected[from] {
		// こちらの計画より先に相手が offer を出してきた場合。近くにいて空きがあれば受け入れる
		d, near := c.neighbors[from]
		if !near || d > DropRadius || len(c.planner.connected) >= MaxPeers {
			return
		}
		c.planner.connected[from] = true
		c.eng.SetGain(from, Gain(d))
	}
	c.eng.Signal(from, payload)
}

// OnSignal は Handler の実装。
func (c *Controller) OnSignal(to uint32, payload []byte) { c.send(to, payload) }

// OnPeerClosed は Handler の実装。しばらく待ってから、近くにいればまたつなぐ。
func (c *Controller) OnPeerClosed(pid uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.planner.connected, pid)
	c.cooldown[pid] = c.now().Add(retryAfter)
}
