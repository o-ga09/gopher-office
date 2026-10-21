package client

import (
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/o-ga09/gopher-office/internal/sim"
)

const (
	screenW = 1280
	screenH = 720

	// tilePx は 1 タイルの描画サイズ（px）。
	tilePx = 32
	// mapOffsetY はマップを縦方向の中央に置くためのオフセット。
	mapOffsetY = (screenH - 22*tilePx) / 2
	// subToPx はサブユニット座標を px に変換する係数（1 タイル = 256 サブユニット = 32px）。
	subToPx = float64(tilePx) / float64(sim.SubPerTile)

	spriteW, spriteH = 48, 48
)

func rgb(r, g, b uint8) color.Color { return color.RGBA{r, g, b, 0xff} }

// bodyColor は性別ごとの Gopher の体の色。
func bodyColor(g sim.Gender) color.Color {
	switch g {
	case sim.GenderMale:
		return rgb(0x4a, 0x90, 0xd9) // 青
	case sim.GenderFemale:
		return rgb(0xe5, 0x48, 0x4d) // 赤
	default:
		return rgb(0xf5, 0xc5, 0x18) // 黄
	}
}

func genderLabel(g sim.Gender) string {
	switch g {
	case sim.GenderMale:
		return "Male"
	case sim.GenderFemale:
		return "Female"
	default:
		return "Other"
	}
}

func darken(c color.Color, f float64) color.Color {
	r, g, b, _ := c.RGBA()
	return color.RGBA{uint8(float64(r>>8) * f), uint8(float64(g>>8) * f), uint8(float64(b>>8) * f), 0xff}
}

// newGopherSprite は仮の Gopher スプライトを図形で描く。素材が用意できたら差し替える。
// 足元（下端中央）が座標の基準点。
func newGopherSprite(body color.Color) *ebiten.Image {
	img := ebiten.NewImage(spriteW, spriteH)
	dark := darken(body, 0.72)
	snout := rgb(0xf2, 0xdd, 0xb5)
	white := rgb(0xff, 0xff, 0xff)
	black := rgb(0x20, 0x20, 0x28)

	// 足
	vector.FillCircle(img, 16, 44, 4, dark, true)
	vector.FillCircle(img, 32, 44, 4, dark, true)
	// 耳
	vector.FillCircle(img, 12, 10, 5, dark, true)
	vector.FillCircle(img, 36, 10, 5, dark, true)
	// 体と頭
	vector.FillCircle(img, 24, 30, 15, body, true)
	vector.FillCircle(img, 24, 22, 15, body, true)
	// 目
	vector.FillCircle(img, 17, 20, 5, white, true)
	vector.FillCircle(img, 31, 20, 5, white, true)
	vector.FillCircle(img, 18, 21, 2, black, true)
	vector.FillCircle(img, 30, 21, 2, black, true)
	// 口まわり
	vector.FillCircle(img, 24, 27, 6, snout, true)
	vector.FillCircle(img, 24, 25, 2, black, true)
	vector.FillRect(img, 22, 29, 2, 3, white, false)
	vector.FillRect(img, 25, 29, 2, 3, white, false)
	return img
}

// newBackground はオフィスのマップを 1 枚の画像に事前合成する。
func newBackground(m *sim.Map) *ebiten.Image {
	img := ebiten.NewImage(m.Width*tilePx, m.Height*tilePx)
	for y := 0; y < m.Height; y++ {
		for x := 0; x < m.Width; x++ {
			drawTile(img, m.At(x, y), x, y)
		}
	}
	return img
}

func drawTile(dst *ebiten.Image, t sim.Tile, x, y int) {
	px, py := float32(x*tilePx), float32(y*tilePx)
	size := float32(tilePx)
	checker := (x+y)%2 == 0

	switch t {
	case sim.TileFloor:
		c := rgb(0xe8, 0xe2, 0xd6)
		if checker {
			c = rgb(0xe2, 0xdb, 0xcd)
		}
		vector.FillRect(dst, px, py, size, size, c, false)
	case sim.TileCarpet:
		c := rgb(0x6b, 0x7f, 0xa8)
		if checker {
			c = rgb(0x66, 0x7a, 0xa2)
		}
		vector.FillRect(dst, px, py, size, size, c, false)
	case sim.TileWall:
		vector.FillRect(dst, px, py, size, size, rgb(0x3a, 0x3f, 0x4b), false)
		vector.FillRect(dst, px, py, size, 4, rgb(0x55, 0x5b, 0x6b), false)
	case sim.TileDesk:
		vector.FillRect(dst, px, py, size, size, rgb(0xa9, 0x78, 0x4f), false)
		vector.FillRect(dst, px, py, size, 3, rgb(0xc2, 0x8f, 0x5e), false)
		vector.StrokeRect(dst, px+0.5, py+0.5, size-1, size-1, 1, rgb(0x8a, 0x5f, 0x3c), false)
	case sim.TilePlant:
		vector.FillRect(dst, px, py, size, size, rgb(0xe8, 0xe2, 0xd6), false)
		vector.FillRect(dst, px+10, py+20, 12, 10, rgb(0x9a, 0x5b, 0x3a), false)
		vector.FillCircle(dst, px+16, py+14, 11, rgb(0x3f, 0x9b, 0x55), true)
		vector.FillCircle(dst, px+12, py+11, 6, rgb(0x56, 0xb6, 0x6c), true)
	}
}
