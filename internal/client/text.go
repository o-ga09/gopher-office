package client

import (
	"bytes"
	"image/color"
	"log"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"golang.org/x/image/font/gofont/goregular"
)

var fontSource = func() *text.GoTextFaceSource {
	s, err := text.NewGoTextFaceSource(bytes.NewReader(goregular.TTF))
	if err != nil {
		log.Fatal(err)
	}
	return s
}()

func face(size float64) *text.GoTextFace {
	return &text.GoTextFace{Source: fontSource, Size: size}
}

func textSize(s string, size float64) (w, h float64) { return text.Measure(s, face(size), 0) }

type align int

const (
	alignLeft align = iota
	alignCenter
)

// drawText は (x, y) を基準にテキストを描く。alignCenter のときは x がテキストの中心。y は上端。
func drawText(dst *ebiten.Image, s string, size float64, x, y float64, a align, clr color.Color) {
	op := &text.DrawOptions{}
	op.LineSpacing = size * 1.5 // 既定は 0 で、複数行が重なる
	if a == alignCenter {
		op.PrimaryAlign = text.AlignCenter // 各行を x を中心に揃える
	}
	op.GeoM.Translate(x, y)
	op.ColorScale.ScaleWithColor(clr)
	text.Draw(dst, s, face(size), op)
}

// 名前タグは描画のたびに文字を組むと重い（数百人の画面で支配的になる）ので、画像にしてキャッシュする。
type nameTagKey struct {
	name string
	self bool
}

// maxNameTags はキャッシュする名前タグの上限。超えたら作り直す（入退室が多いときにメモリが増え続けないように）。
const maxNameTags = 2000

func (g *Game) nameTag(name string, self bool) *ebiten.Image {
	k := nameTagKey{name, self}
	if img, ok := g.nameTags[k]; ok {
		return img
	}
	if len(g.nameTags) >= maxNameTags {
		for _, img := range g.nameTags {
			img.Deallocate()
		}
		clear(g.nameTags)
	}

	const size = 13.0
	w, h := textSize(name, size)
	img := ebiten.NewImage(int(w)+10, int(h)+4)
	bg := color.RGBA{0x10, 0x14, 0x20, 0xb0}
	fg := color.RGBA{0xff, 0xff, 0xff, 0xff}
	if self {
		bg = color.RGBA{0x1f, 0x4f, 0x8f, 0xd0} // 自分は色を変えて見つけやすくする
	}
	img.Fill(bg)
	drawText(img, name, size, float64(img.Bounds().Dx())/2, 2, alignCenter, fg)
	g.nameTags[k] = img
	return img
}

// Gopher のクレジット（CC BY 3.0、Must）。画面中央に常時表示し、閉じる操作は設けない。
const (
	attributionLine1 = "The Go gopher was designed by Renée French."
	attributionLine2 = "Licensed under CC BY 3.0 (https://creativecommons.org/licenses/by/3.0/) - recolored."
)

// drawCredit は Gopher のクレジットを画面中央に描く。必ずシーンの最前面（最後）に呼ぶこと。
func drawCredit(dst *ebiten.Image) {
	const (
		size = 14.0
		padX = 16.0
		padY = 10.0
		gap  = 4.0
	)
	w1, h := text.Measure(attributionLine1, face(size), 0)
	w2, _ := text.Measure(attributionLine2, face(size), 0)
	w := max(w1, w2)

	boxW, boxH := w+padX*2, h*2+gap+padY*2
	boxX, boxY := (screenW-boxW)/2, (screenH-boxH)/2
	vector.FillRect(dst, float32(boxX), float32(boxY), float32(boxW), float32(boxH), color.RGBA{0x10, 0x14, 0x20, 0xc8}, false)
	vector.StrokeRect(dst, float32(boxX), float32(boxY), float32(boxW), float32(boxH), 1, color.RGBA{0xff, 0xff, 0xff, 0x50}, false)

	white := color.RGBA{0xff, 0xff, 0xff, 0xff}
	drawText(dst, attributionLine1, size, screenW/2, boxY+padY, alignCenter, white)
	drawText(dst, attributionLine2, size, screenW/2, boxY+padY+h+gap, alignCenter, white)
}
