package sim

import "math"

const (
	// SpeedSub は 1 tick あたりの移動量（サブユニット）。51 ≒ 4 タイル/秒（20Hz）。
	SpeedSub = 51
	// halfSize は当たり判定の半径（サブユニット）。0.3 タイル。
	halfSize = 77
)

// Gender はプロフィールの性別。値は DB の players.gender と一致させる。
type Gender uint8

const (
	GenderMale   Gender = iota // 男性（青 Gopher）
	GenderFemale               // 女性（赤 Gopher）
	GenderOther                // その他（黄 Gopher）
)

// Actor は 1 体のアバターの移動状態。位置はサブユニット（1 タイル = 256）の整数で持つ。
type Actor struct {
	X, Y       int
	dirX, dirY int
	path       []Point // 向かう先のタイル列（先頭が次の目的地）
	target     Point
	hasTarget  bool
}

// NewActor は指定タイルの中心に Actor を置く。
func NewActor(t Point) *Actor {
	return &Actor{X: tileCenter(t.X), Y: tileCenter(t.Y)}
}

// NewActorAt はサブユニット座標を直接指定して Actor を作る（サーバから受け取った位置の再現用）。
func NewActorAt(x, y int) *Actor { return &Actor{X: x, Y: y} }

func tileCenter(t int) int { return t<<SubShift + SubPerTile/2 }

// Teleport は移動の意図（経路・方向）を保ったまま、位置だけを動かす（サーバの位置への補正用）。
func (a *Actor) Teleport(x, y int) { a.X, a.Y = x, y }

// Dir は現在の方向入力を返す。
func (a *Actor) Dir() (dx, dy int) { return a.dirX, a.dirY }

// Tile は現在いるタイルを返す。
func (a *Actor) Tile() Point { return Point{a.X >> SubShift, a.Y >> SubShift} }

// Moving は移動中かを返す。
func (a *Actor) Moving() bool { return a.dirX != 0 || a.dirY != 0 || len(a.path) > 0 }

// Target はクリック移動の最終目的地を返す。移動中でなければ ok=false。
func (a *Actor) Target() (p Point, ok bool) { return a.target, a.hasTarget && len(a.path) > 0 }

// SetDir は方向入力を設定する（各成分は -1, 0, 1 に丸める）。
// 非ゼロを渡すとクリック移動の経路は破棄される。
func (a *Actor) SetDir(dx, dy int) {
	a.dirX, a.dirY = clamp1(dx), clamp1(dy)
	if a.dirX != 0 || a.dirY != 0 {
		a.path, a.hasTarget = nil, false
	}
}

// MoveTo は指定タイルまでの経路を探索して移動を開始する。到達できなければ false。
func (a *Actor) MoveTo(m *Map, t Point) bool {
	start := a.Tile()
	path, ok := FindPath(m, start, t)
	if !ok {
		return false
	}
	if a.X == tileCenter(start.X) && a.Y == tileCenter(start.Y) {
		a.path = path
	} else {
		// タイルの中心から外れている場合は、まず現在のタイルの中心へ寄ってから経路をたどる
		a.path = append([]Point{start}, path...)
	}
	a.target, a.hasTarget = t, true
	a.dirX, a.dirY = 0, 0
	return true
}

// Stop は移動を止める。
func (a *Actor) Stop() {
	a.dirX, a.dirY = 0, 0
	a.path, a.hasTarget = nil, false
}

// Step は 1 tick 分だけ移動を進める。
func (a *Actor) Step(m *Map) {
	switch {
	case a.dirX != 0 || a.dirY != 0:
		ds := SpeedSub
		if a.dirX != 0 && a.dirY != 0 {
			ds = int(math.Round(SpeedSub * math.Sqrt2 / 2))
		}
		// 軸ごとに分けて動かし、壁に沿って滑れるようにする
		a.tryMove(m, a.dirX*ds, 0)
		a.tryMove(m, 0, a.dirY*ds)
	case len(a.path) > 0:
		wp := a.path[0]
		dx, dy := tileCenter(wp.X)-a.X, tileCenter(wp.Y)-a.Y
		dist := math.Hypot(float64(dx), float64(dy))
		if dist <= SpeedSub {
			a.X, a.Y = tileCenter(wp.X), tileCenter(wp.Y)
			a.path = a.path[1:]
			return
		}
		a.X += int(math.Round(float64(dx) / dist * SpeedSub))
		a.Y += int(math.Round(float64(dy) / dist * SpeedSub))
	}
}

// tryMove は壁に当たる場合に移動量を縮めながら、動けるところまで動かす。
func (a *Actor) tryMove(m *Map, dx, dy int) {
	for dx != 0 || dy != 0 {
		if canStand(m, a.X+dx, a.Y+dy) {
			a.X += dx
			a.Y += dy
			return
		}
		dx, dy = shrink(dx), shrink(dy)
	}
}

// canStand は当たり判定の四隅がすべて歩けるタイル上にあるかを返す。
func canStand(m *Map, x, y int) bool {
	for _, c := range [...][2]int{
		{x - halfSize, y - halfSize}, {x + halfSize, y - halfSize},
		{x - halfSize, y + halfSize}, {x + halfSize, y + halfSize},
	} {
		if !m.Walkable(c[0]>>SubShift, c[1]>>SubShift) {
			return false
		}
	}
	return true
}

func clamp1(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

func shrink(v int) int {
	switch {
	case v > 0:
		return v - 1
	case v < 0:
		return v + 1
	}
	return 0
}
