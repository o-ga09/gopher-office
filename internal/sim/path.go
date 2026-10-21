package sim

import "container/heap"

const (
	costOrth = 10
	costDiag = 14
)

var neighbors = [...]struct{ dx, dy, cost int }{
	{1, 0, costOrth}, {-1, 0, costOrth}, {0, 1, costOrth}, {0, -1, costOrth},
	{1, 1, costDiag}, {1, -1, costDiag}, {-1, 1, costDiag}, {-1, -1, costDiag},
}

type pathNode struct{ idx, f, g int }

type pathHeap []pathNode

func (h pathHeap) Len() int { return len(h) }
func (h pathHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.f != b.f {
		return a.f < b.f
	}
	if a.g != b.g {
		return a.g > b.g
	}
	return a.idx < b.idx // 同コストでも結果が決定的になるようにする
}
func (h pathHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *pathHeap) Push(x any)   { *h = append(*h, x.(pathNode)) }
func (h *pathHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

func octile(a, b Point) int {
	dx, dy := abs(a.X-b.X), abs(a.Y-b.Y)
	if dx < dy {
		dx, dy = dy, dx
	}
	return costOrth*dx + (costDiag-costOrth)*dy
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// FindPath は from から to までの 8 方向グリッド A* 経路を返す。
// 戻り値は from を含まず to を含むタイル列。from == to なら空の経路で ok=true。
// 斜め移動は、隣接する縦横の両タイルが歩ける場合のみ許可する（角抜け防止）。
func FindPath(m *Map, from, to Point) (path []Point, ok bool) {
	if !m.Walkable(from.X, from.Y) || !m.Walkable(to.X, to.Y) {
		return nil, false
	}
	if from == to {
		return nil, true
	}

	const unvisited = int(^uint(0) >> 1)
	n := m.Width * m.Height
	gScore := make([]int, n)
	parent := make([]int, n)
	for i := range gScore {
		gScore[i] = unvisited
		parent[i] = -1
	}

	start := from.Y*m.Width + from.X
	goal := to.Y*m.Width + to.X
	gScore[start] = 0
	open := &pathHeap{{idx: start, f: octile(from, to), g: 0}}

	for open.Len() > 0 {
		cur := heap.Pop(open).(pathNode)
		if cur.g > gScore[cur.idx] {
			continue // より良い経路で更新済み
		}
		if cur.idx == goal {
			break
		}
		cx, cy := cur.idx%m.Width, cur.idx/m.Width
		for _, d := range neighbors {
			nx, ny := cx+d.dx, cy+d.dy
			if !m.Walkable(nx, ny) {
				continue
			}
			if d.dx != 0 && d.dy != 0 && (!m.Walkable(cx+d.dx, cy) || !m.Walkable(cx, cy+d.dy)) {
				continue
			}
			ni := ny*m.Width + nx
			g := cur.g + d.cost
			if g >= gScore[ni] {
				continue
			}
			gScore[ni] = g
			parent[ni] = cur.idx
			heap.Push(open, pathNode{idx: ni, f: g + octile(Point{nx, ny}, to), g: g})
		}
	}

	if parent[goal] == -1 {
		return nil, false
	}
	for i := goal; i != start; i = parent[i] {
		path = append(path, Point{i % m.Width, i / m.Width})
	}
	for l, r := 0, len(path)-1; l < r; l, r = l+1, r-1 {
		path[l], path[r] = path[r], path[l]
	}
	return path, true
}
