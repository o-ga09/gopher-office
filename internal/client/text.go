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
	f := face(size)
	if a == alignCenter {
		w, _ := text.Measure(s, f, 0)
		x -= w / 2
	}
	op := &text.DrawOptions{}
	op.GeoM.Translate(x, y)
	op.ColorScale.ScaleWithColor(clr)
	text.Draw(dst, s, f, op)
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
