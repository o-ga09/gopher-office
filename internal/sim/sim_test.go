package sim

import "testing"

func mustMap(t *testing.T, rows ...string) *Map {
	t.Helper()
	m, err := NewMapFromASCII(rows)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNewMapFromASCII(t *testing.T) {
	m := mustMap(t,
		"#####",
		"#.,D#",
		"#P..#",
		"#####",
	)
	if m.Width != 5 || m.Height != 4 {
		t.Fatalf("size = %dx%d", m.Width, m.Height)
	}
	cases := []struct {
		p    Point
		want Tile
	}{{Point{1, 1}, TileFloor}, {Point{2, 1}, TileCarpet}, {Point{3, 1}, TileDesk}, {Point{1, 2}, TilePlant}, {Point{0, 0}, TileWall}}
	for _, c := range cases {
		if got := m.At(c.p.X, c.p.Y); got != c.want {
			t.Errorf("At(%v) = %v, want %v", c.p, got, c.want)
		}
	}
	if m.Walkable(-1, 0) || m.Walkable(5, 0) {
		t.Error("out of range must not be walkable")
	}
}

func TestNewMapFromASCIIErrors(t *testing.T) {
	if _, err := NewMapFromASCII(nil); err == nil {
		t.Error("empty map must fail")
	}
	if _, err := NewMapFromASCII([]string{"###", "##"}); err == nil {
		t.Error("ragged rows must fail")
	}
	if _, err := NewMapFromASCII([]string{"#?#"}); err == nil {
		t.Error("unknown tile must fail")
	}
}

func TestOfficeMap(t *testing.T) {
	m := OfficeMap()
	if m.Width != 40 || m.Height != 22 {
		t.Fatalf("size = %dx%d", m.Width, m.Height)
	}
	if !m.Walkable(OfficeSpawn.X, OfficeSpawn.Y) {
		t.Fatal("spawn must be walkable")
	}
	// 外周はすべて壁
	for x := 0; x < m.Width; x++ {
		if m.Walkable(x, 0) || m.Walkable(x, m.Height-1) {
			t.Fatalf("border not wall at x=%d", x)
		}
	}
	// 歩ける場所がすべてスポーンから到達できる（孤立した区画がない）
	for y := 0; y < m.Height; y++ {
		for x := 0; x < m.Width; x++ {
			if !m.Walkable(x, y) {
				continue
			}
			if _, ok := FindPath(m, OfficeSpawn, Point{x, y}); !ok {
				t.Errorf("(%d,%d) is unreachable from spawn", x, y)
			}
		}
	}
}

func TestFindPathAroundWall(t *testing.T) {
	m := mustMap(t,
		"#######",
		"#..#..#",
		"#..#..#",
		"#.....#",
		"#######",
	)
	path, ok := FindPath(m, Point{1, 1}, Point{5, 1})
	if !ok {
		t.Fatal("path not found")
	}
	if last := path[len(path)-1]; last != (Point{5, 1}) {
		t.Fatalf("last = %v", last)
	}
	prev := Point{1, 1}
	for _, p := range path {
		if !m.Walkable(p.X, p.Y) {
			t.Fatalf("path goes through non-walkable %v", p)
		}
		if abs(p.X-prev.X) > 1 || abs(p.Y-prev.Y) > 1 {
			t.Fatalf("non-adjacent step %v -> %v", prev, p)
		}
		prev = p
	}
}

func TestFindPathNoCornerCutting(t *testing.T) {
	m := mustMap(t,
		"####",
		"#.##",
		"##.#",
		"####",
	)
	// (1,1) と (2,2) は斜めに接しているが、縦横の隣が壁なので通れない
	if _, ok := FindPath(m, Point{1, 1}, Point{2, 2}); ok {
		t.Fatal("must not cut a corner between two walls")
	}
}

func TestFindPathEdgeCases(t *testing.T) {
	m := mustMap(t,
		"#####",
		"#.D.#",
		"#####",
	)
	if p, ok := FindPath(m, Point{1, 1}, Point{1, 1}); !ok || len(p) != 0 {
		t.Errorf("same tile: path=%v ok=%v", p, ok)
	}
	if _, ok := FindPath(m, Point{1, 1}, Point{2, 1}); ok {
		t.Error("goal on a desk must fail")
	}
	if _, ok := FindPath(m, Point{1, 1}, Point{3, 1}); ok {
		t.Error("unreachable goal must fail")
	}
}

func TestFindPathDeterministic(t *testing.T) {
	m := OfficeMap()
	a, _ := FindPath(m, OfficeSpawn, Point{2, 2})
	b, _ := FindPath(m, OfficeSpawn, Point{2, 2})
	if len(a) != len(b) {
		t.Fatal("length differs")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("differs at %d", i)
		}
	}
}

func TestActorDirMoves(t *testing.T) {
	m := mustMap(t,
		"#######",
		"#.....#",
		"#######",
	)
	a := NewActor(Point{1, 1})
	start := a.X
	a.SetDir(1, 0)
	for i := 0; i < 10; i++ {
		a.Step(m)
	}
	if got := a.X - start; got != 10*SpeedSub {
		t.Fatalf("moved %d, want %d", got, 10*SpeedSub)
	}
}

func TestActorStopsAtWall(t *testing.T) {
	m := mustMap(t,
		"#####",
		"#...#",
		"#####",
	)
	a := NewActor(Point{1, 1})
	a.SetDir(1, 0)
	for i := 0; i < 200; i++ {
		a.Step(m)
	}
	if !canStand(m, a.X, a.Y) {
		t.Fatalf("actor is inside a wall at (%d,%d)", a.X, a.Y)
	}
	if a.Tile() != (Point{3, 1}) {
		t.Fatalf("tile = %v, want (3,1)", a.Tile())
	}
	// 右端の壁に隙間なく付いている（0.3 タイルの当たり半径ぶん手前）
	if want := 4<<SubShift - halfSize - 1; a.X != want {
		t.Fatalf("x = %d, want %d", a.X, want)
	}
}

func TestActorSlidesAlongWall(t *testing.T) {
	m := mustMap(t,
		"#####",
		"#...#",
		"#...#",
		"#...#",
		"#####",
	)
	a := NewActor(Point{1, 1})
	a.SetDir(-1, 1) // 左の壁に押しつけながら下へ
	for i := 0; i < 30; i++ {
		a.Step(m)
	}
	if a.Tile().Y < 3 {
		t.Fatalf("did not slide down: %v", a.Tile())
	}
}

func TestActorMoveToReachesTarget(t *testing.T) {
	m := OfficeMap()
	a := NewActor(OfficeSpawn)
	goal := Point{2, 2}
	if !a.MoveTo(m, goal) {
		t.Fatal("MoveTo failed")
	}
	if p, ok := a.Target(); !ok || p != goal {
		t.Fatalf("Target = %v %v", p, ok)
	}
	for i := 0; i < 2000 && a.Moving(); i++ {
		a.Step(m)
		if !canStand(m, a.X, a.Y) {
			t.Fatalf("actor entered a wall at tick %d", i)
		}
	}
	if a.Moving() {
		t.Fatal("did not arrive")
	}
	if a.Tile() != goal {
		t.Fatalf("tile = %v, want %v", a.Tile(), goal)
	}
	if a.X != tileCenter(goal.X) || a.Y != tileCenter(goal.Y) {
		t.Fatal("must stop exactly at the tile center")
	}
	if _, ok := a.Target(); ok {
		t.Fatal("Target must be cleared on arrival")
	}
}

func TestActorMoveToUnreachable(t *testing.T) {
	m := OfficeMap()
	a := NewActor(OfficeSpawn)
	if a.MoveTo(m, Point{0, 0}) {
		t.Fatal("MoveTo wall must fail")
	}
	if a.Moving() {
		t.Fatal("failed MoveTo must not start moving")
	}
}

func TestActorDirCancelsPath(t *testing.T) {
	m := OfficeMap()
	a := NewActor(OfficeSpawn)
	a.MoveTo(m, Point{2, 2})
	a.SetDir(1, 0)
	if _, ok := a.Target(); ok {
		t.Fatal("direction input must cancel click-move")
	}
	a.SetDir(0, 0)
	if a.Moving() {
		t.Fatal("must be idle")
	}
}

func TestActorMoveToFromOffCenter(t *testing.T) {
	m := OfficeMap()
	a := NewActor(OfficeSpawn)
	a.SetDir(1, 0)
	for i := 0; i < 3; i++ {
		a.Step(m)
	}
	a.SetDir(0, 0)
	goal := Point{OfficeSpawn.X, 15}
	if !a.MoveTo(m, goal) {
		t.Fatal("MoveTo failed")
	}
	for i := 0; i < 1000 && a.Moving(); i++ {
		a.Step(m)
		if !canStand(m, a.X, a.Y) {
			t.Fatalf("actor entered a wall at tick %d", i)
		}
	}
	if a.Tile() != goal {
		t.Fatalf("tile = %v, want %v", a.Tile(), goal)
	}
}

func TestActorDeterministic(t *testing.T) {
	run := func() (int, int) {
		m := OfficeMap()
		a := NewActor(OfficeSpawn)
		a.MoveTo(m, Point{2, 2})
		for i := 0; i < 123; i++ {
			a.Step(m)
		}
		return a.X, a.Y
	}
	x1, y1 := run()
	x2, y2 := run()
	if x1 != x2 || y1 != y2 {
		t.Fatal("simulation is not deterministic")
	}
}

func TestNewActorAtAndDir(t *testing.T) {
	a := NewActorAt(300, 400)
	if a.X != 300 || a.Y != 400 {
		t.Fatalf("pos = (%d,%d)", a.X, a.Y)
	}
	a.SetDir(5, -3) // 範囲外の値は -1, 0, 1 に丸められる
	if dx, dy := a.Dir(); dx != 1 || dy != -1 {
		t.Fatalf("dir = (%d,%d)", dx, dy)
	}
}

func TestWalkableTiles(t *testing.T) {
	m := mustMap(t,
		"####",
		"#.,#",
		"#D.#",
		"####",
	)
	got := m.WalkableTiles()
	want := []Point{{1, 1}, {2, 1}, {2, 2}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestActorMoveToStartsMovingImmediatelyFromTileCenter(t *testing.T) {
	m := OfficeMap()
	a := NewActor(OfficeSpawn)
	x, y := a.X, a.Y
	a.MoveTo(m, Point{X: 2, Y: 2})
	a.Step(m)
	if a.X == x && a.Y == y {
		t.Fatal("must move on the very first step when starting from a tile center")
	}
}
