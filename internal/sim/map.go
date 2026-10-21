// Package sim は ebiten に依存しない純粋なシミュレーションロジック（マップ・衝突・経路探索・移動）。
// クライアントとサーバで共有する。時間は整数 tick で進め、time.Now() には依存しない。
package sim

import "fmt"

const (
	// TickRate はシミュレーションの周波数（Hz）。
	TickRate = 20
	// SubShift / SubPerTile は座標の分解能。1 タイル = 256 サブユニット。
	SubShift   = 8
	SubPerTile = 1 << SubShift
)

// Tile はマップ上の 1 マスの種類。
type Tile uint8

const (
	TileFloor  Tile = iota // 床（歩ける）
	TileCarpet             // カーペット（歩ける）
	TileWall               // 壁
	TileDesk               // 机・ソファなど（歩けない）
	TilePlant              // 観葉植物（歩けない）
)

// Walkable は歩ける種類のタイルかを返す。
func (t Tile) Walkable() bool { return t == TileFloor || t == TileCarpet }

// Point はタイル座標。
type Point struct{ X, Y int }

// Map は固定の矩形タイルマップ。
type Map struct {
	Width, Height int
	tiles         []Tile
}

// NewMap は全面が床のマップを作る。
func NewMap(w, h int) *Map {
	return &Map{Width: w, Height: h, tiles: make([]Tile, w*h)}
}

// NewMapFromASCII は ASCII 表現からマップを作る。
// '.' 床 / ',' カーペット / '#' 壁 / 'D' 机 / 'P' 観葉植物。
func NewMapFromASCII(rows []string) (*Map, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("sim: empty map")
	}
	m := NewMap(len(rows[0]), len(rows))
	for y, row := range rows {
		if len(row) != m.Width {
			return nil, fmt.Errorf("sim: row %d has width %d, want %d", y, len(row), m.Width)
		}
		for x, c := range row {
			switch c {
			case '.':
				m.Set(x, y, TileFloor)
			case ',':
				m.Set(x, y, TileCarpet)
			case '#':
				m.Set(x, y, TileWall)
			case 'D':
				m.Set(x, y, TileDesk)
			case 'P':
				m.Set(x, y, TilePlant)
			default:
				return nil, fmt.Errorf("sim: unknown tile %q at (%d,%d)", c, x, y)
			}
		}
	}
	return m, nil
}

// In は座標がマップ内かを返す。
func (m *Map) In(x, y int) bool { return x >= 0 && y >= 0 && x < m.Width && y < m.Height }

// At はタイルを返す。範囲外は壁として扱う。
func (m *Map) At(x, y int) Tile {
	if !m.In(x, y) {
		return TileWall
	}
	return m.tiles[y*m.Width+x]
}

// Set はタイルを書き換える。範囲外は無視する。
func (m *Map) Set(x, y int, t Tile) {
	if m.In(x, y) {
		m.tiles[y*m.Width+x] = t
	}
}

// Walkable は指定タイルが歩けるかを返す。
func (m *Map) Walkable(x, y int) bool { return m.At(x, y).Walkable() }

func (m *Map) fill(x0, y0, x1, y1 int, t Tile) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			m.Set(x, y, t)
		}
	}
}

// OfficeSpawn はオフィスマップのスポーン位置。
var OfficeSpawn = Point{X: 20, Y: 19}

// OfficeMap は v0 で使う固定のオフィスマップ（40×22）を返す。
func OfficeMap() *Map {
	m := NewMap(40, 22)

	// 外壁
	m.fill(0, 0, 39, 0, TileWall)
	m.fill(0, 21, 39, 21, TileWall)
	m.fill(0, 0, 0, 21, TileWall)
	m.fill(39, 0, 39, 21, TileWall)

	// 会議室（左上）: カーペット、壁、ドア 2 か所、中央にテーブル
	m.fill(1, 1, 12, 7, TileCarpet)
	m.fill(13, 0, 13, 8, TileWall)
	m.fill(0, 8, 13, 8, TileWall)
	m.fill(13, 4, 13, 5, TileFloor) // 右側のドア
	m.fill(6, 8, 7, 8, TileFloor)   // 下側のドア
	m.fill(4, 3, 9, 4, TileDesk)

	// 執務エリア（右）: 机のブロックを 3×3 で並べる
	for _, y := range []int{3, 8, 13} {
		for _, x := range []int{17, 24, 31} {
			m.fill(x, y, x+3, y+1, TileDesk)
		}
	}

	// ラウンジ（左下）: カーペットとソファ
	m.fill(1, 10, 12, 20, TileCarpet)
	m.fill(2, 19, 5, 19, TileDesk)
	m.fill(8, 12, 10, 13, TileDesk)

	// 観葉植物
	for _, p := range []Point{{1, 1}, {12, 1}, {1, 9}, {12, 9}, {38, 1}, {38, 20}, {14, 20}, {1, 20}} {
		m.Set(p.X, p.Y, TilePlant)
	}

	return m
}
