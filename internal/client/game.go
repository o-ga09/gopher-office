// Package client は Ebitengine のクライアント（描画・入力・画面遷移）。ロジックは internal/sim に置く。
package client

import (
	"image"
	"image/color"
	"sort"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/o-ga09/gopher-office/internal/sim"
)

type scene int

const (
	sceneSelect scene = iota // 性別（Gopher の色）選択
	sceneOffice              // オフィス
)

// framesPerTick は描画フレーム（60Hz）あたりのシミュレーション tick（20Hz）の比。
const framesPerTick = 60 / sim.TickRate

// Game は ebiten.Game の実装。
type Game struct {
	scene scene

	gender  sim.Gender
	sprites [3]*ebiten.Image

	m  *sim.Map
	bg *ebiten.Image

	me           *sim.Actor
	prevX, prevY int // 直前の tick の位置（補間用）
	frame        int // 現在の tick 内の経過フレーム
	pendingClick *sim.Point
	keys         [4]bool // 今の tick の間に押された移動キー（A, D, W, S）
	hoverCard    int
	cardRects    [3]image.Rectangle
}

// NewGame は選択画面から始まる Game を作る。
func NewGame() *Game {
	g := &Game{m: sim.OfficeMap(), hoverCard: -1}
	g.bg = newBackground(g.m)
	for i := range g.sprites {
		g.sprites[i] = newGopherSprite(bodyColor(sim.Gender(i)))
	}
	const cardW, cardH, gap = 220, 170, 40
	x0 := (screenW - (3*cardW + 2*gap)) / 2
	for i := range g.cardRects {
		x := x0 + i*(cardW+gap)
		g.cardRects[i] = image.Rect(x, 130, x+cardW, 130+cardH)
	}
	return g
}

func (g *Game) Layout(_, _ int) (int, int) { return screenW, screenH }

func (g *Game) Update() error {
	switch g.scene {
	case sceneSelect:
		g.updateSelect()
	case sceneOffice:
		g.updateOffice()
	}
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	switch g.scene {
	case sceneSelect:
		g.drawSelect(screen)
	case sceneOffice:
		g.drawOffice(screen)
	}
	drawCredit(screen) // 常に最前面・画面中央
}

// --- 性別選択 ---

func (g *Game) updateSelect() {
	g.hoverCard = -1
	mx, my := ebiten.CursorPosition()
	for i, r := range g.cardRects {
		if image.Pt(mx, my).In(r) {
			g.hoverCard = i
		}
	}

	choice := -1
	switch {
	case inpututil.IsKeyJustPressed(ebiten.Key1):
		choice = 0
	case inpututil.IsKeyJustPressed(ebiten.Key2):
		choice = 1
	case inpututil.IsKeyJustPressed(ebiten.Key3):
		choice = 2
	}
	if click, ok := justClicked(); ok {
		for i, r := range g.cardRects {
			if click.In(r) {
				choice = i
			}
		}
	}
	if choice >= 0 {
		g.enterOffice(sim.Gender(choice))
	}
}

func (g *Game) enterOffice(gender sim.Gender) {
	g.gender = gender
	g.me = sim.NewActor(sim.OfficeSpawn)
	g.prevX, g.prevY = g.me.X, g.me.Y
	g.frame, g.pendingClick = 0, nil
	g.scene = sceneOffice
}

func (g *Game) drawSelect(screen *ebiten.Image) {
	screen.Fill(color.RGBA{0x1b, 0x20, 0x30, 0xff})
	white := color.RGBA{0xff, 0xff, 0xff, 0xff}
	drawText(screen, "Gopher Office", 40, screenW/2, 40, alignCenter, white)
	drawText(screen, "Choose your Gopher", 18, screenW/2, 96, alignCenter, color.RGBA{0xb8, 0xc0, 0xd8, 0xff})

	for i, r := range g.cardRects {
		bg := color.RGBA{0x26, 0x2d, 0x44, 0xff}
		if i == g.hoverCard {
			bg = color.RGBA{0x34, 0x3e, 0x5e, 0xff}
		}
		vector.FillRect(screen, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), bg, false)
		vector.StrokeRect(screen, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), 2, bodyColor(sim.Gender(i)), false)

		op := &ebiten.DrawImageOptions{}
		op.GeoM.Scale(2.2, 2.2)
		op.GeoM.Translate(float64(r.Min.X)+(float64(r.Dx())-spriteW*2.2)/2, float64(r.Min.Y)+10)
		screen.DrawImage(g.sprites[i], op)

		label := genderLabel(sim.Gender(i)) + " [" + string(rune('1'+i)) + "]"
		drawText(screen, label, 18, float64(r.Min.X+r.Dx()/2), float64(r.Max.Y)-32, alignCenter, white)
	}

	drawText(screen, "Click a card or press 1 / 2 / 3", 16, screenW/2, 470, alignCenter, color.RGBA{0xb8, 0xc0, 0xd8, 0xff})
}

// --- オフィス ---

func (g *Game) updateOffice() {
	// 入力は毎フレーム読み取り、tick の境界で sim に反映する
	if click, ok := justClicked(); ok {
		tx, ty := click.X/tilePx, (click.Y-mapOffsetY)/tilePx
		if click.Y >= mapOffsetY && g.m.In(tx, ty) {
			p := sim.Point{X: tx, Y: ty}
			g.pendingClick = &p
		}
	}

	// tick の間に離された短いタップも取りこぼさないよう、押されたキーを tick が来るまで貯める
	for i, k := range moveKeys {
		if ebiten.IsKeyPressed(k) || inpututil.IsKeyJustPressed(k) {
			g.keys[i] = true
		}
	}

	g.frame++
	if g.frame < framesPerTick {
		return
	}
	g.frame = 0
	g.step()
}

// moveKeys は g.keys の添字と対応する移動キー（左・右・上・下）。
var moveKeys = [4]ebiten.Key{ebiten.KeyA, ebiten.KeyD, ebiten.KeyW, ebiten.KeyS}

func (g *Game) step() {
	dx, dy := 0, 0
	if g.keys[0] {
		dx--
	}
	if g.keys[1] {
		dx++
	}
	if g.keys[2] {
		dy--
	}
	if g.keys[3] {
		dy++
	}
	g.keys = [4]bool{}

	switch {
	case dx != 0 || dy != 0:
		g.me.SetDir(dx, dy) // 方向入力はクリック移動より優先
		g.pendingClick = nil
	case g.pendingClick != nil:
		g.me.MoveTo(g.m, *g.pendingClick)
		g.pendingClick = nil
	default:
		g.me.SetDir(0, 0)
	}

	g.prevX, g.prevY = g.me.X, g.me.Y
	g.me.Step(g.m)
}

func (g *Game) drawOffice(screen *ebiten.Image) {
	screen.Fill(color.RGBA{0x14, 0x17, 0x22, 0xff})
	bgOp := &ebiten.DrawImageOptions{}
	bgOp.GeoM.Translate(0, mapOffsetY)
	screen.DrawImage(g.bg, bgOp)

	// クリック先のマーカー
	if t, ok := g.me.Target(); ok {
		cx := float32(t.X*tilePx + tilePx/2)
		cy := float32(t.Y*tilePx + tilePx/2 + mapOffsetY)
		vector.StrokeCircle(screen, cx, cy, 10, 2, color.RGBA{0xff, 0xff, 0xff, 0xb0}, true)
	}

	// 描画位置は直前の tick と現在の tick の間を線形補間する
	alpha := float64(g.frame) / framesPerTick
	px := (float64(g.prevX) + float64(g.me.X-g.prevX)*alpha) * subToPx
	py := (float64(g.prevY)+float64(g.me.Y-g.prevY)*alpha)*subToPx + mapOffsetY

	// 将来、複数アバターを奥から手前の順に描くための入れ物（今は自分だけ）
	type drawable struct {
		x, y   float64
		gender sim.Gender
		name   string
	}
	items := []drawable{{px, py, g.gender, "You"}}
	sort.SliceStable(items, func(i, j int) bool { return items[i].y < items[j].y })

	for _, it := range items {
		op := &ebiten.DrawImageOptions{}
		op.GeoM.Translate(it.x-spriteW/2, it.y-(spriteH-4)) // 足元が座標になるよう合わせる
		screen.DrawImage(g.sprites[it.gender], op)
		g.drawNameTag(screen, it.name, it.x, it.y-spriteH-2)
	}

	drawText(screen, "WASD / Click to move", 14, 12, 4, alignLeft, color.RGBA{0xff, 0xff, 0xff, 0xc0})
}

func (g *Game) drawNameTag(screen *ebiten.Image, name string, cx, y float64) {
	const size = 13.0
	w, h := textSize(name, size)
	boxW, boxH := w+10, h+4
	vector.FillRect(screen, float32(cx-boxW/2), float32(y-boxH), float32(boxW), float32(boxH), color.RGBA{0x10, 0x14, 0x20, 0xb0}, false)
	drawText(screen, name, size, cx, y-boxH+2, alignCenter, color.RGBA{0xff, 0xff, 0xff, 0xff})
}

// justClicked はマウスの左クリックまたはタッチが始まった位置を返す。
func justClicked() (image.Point, bool) {
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		x, y := ebiten.CursorPosition()
		return image.Pt(x, y), true
	}
	if ids := inpututil.AppendJustPressedTouchIDs(nil); len(ids) > 0 {
		x, y := ebiten.TouchPosition(ids[0])
		return image.Pt(x, y), true
	}
	return image.Point{}, false
}
