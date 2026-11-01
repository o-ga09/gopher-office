package client

import (
	"bytes"
	"image"
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
// アイコンの取得状態が変わると別のタグになる（取得中はシルエット、取得後はその画像）。
type nameTagKey struct {
	name  string
	self  bool
	state iconState
	hash  string // state が iconReady のときだけ
}

// maxNameTags はキャッシュする名前タグの上限。超えたら作り直す（入退室が多いときにメモリが増え続けないように）。
const maxNameTags = 2000

// tagIconSize は名前タグの中のアイコンの一辺（px）。
const tagIconSize = 16

// nameTag は名前タグの画像を返す。icon は state が iconReady のときの画像で、hash はそのアイコンのハッシュ。
func (g *Game) nameTag(name string, self bool, state iconState, hash string, icon *ebiten.Image) *ebiten.Image {
	k := nameTagKey{name: name, self: self, state: state}
	if state == iconReady {
		k.hash = hash
	}
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
	iconW := 0
	if state != iconNone {
		iconW = tagIconSize + 4
	}
	img := ebiten.NewImage(int(w)+10+iconW, max(int(h)+4, tagIconSize+4))
	bg := color.RGBA{0x10, 0x14, 0x20, 0xb0}
	fg := color.RGBA{0xff, 0xff, 0xff, 0xff}
	if self {
		bg = color.RGBA{0x1f, 0x4f, 0x8f, 0xd0} // 自分は色を変えて見つけやすくする
	}
	img.Fill(bg)

	switch state {
	case iconReady:
		op := &ebiten.DrawImageOptions{Filter: ebiten.FilterLinear}
		op.GeoM.Scale(tagIconSize/float64(icon.Bounds().Dx()), tagIconSize/float64(icon.Bounds().Dy()))
		op.GeoM.Translate(3, float64(img.Bounds().Dy()-tagIconSize)/2)
		img.DrawImage(icon, op)
	case iconLoading:
		drawSilhouette(img, 3, float32(img.Bounds().Dy()-tagIconSize)/2, tagIconSize)
	}
	textCenter := float64(iconW) + float64(img.Bounds().Dx()-iconW)/2
	drawText(img, name, size, textCenter, float64(img.Bounds().Dy()-int(h))/2, alignCenter, fg)
	g.nameTags[k] = img
	return img
}

// drawSilhouette は、アイコン画像が無い・取得中のときの、人のシルエットを (x, y) に size×size で描く。
func drawSilhouette(dst *ebiten.Image, x, y, size float32) {
	box := image.Rect(int(x), int(y), int(x+size), int(y+size))
	clip := dst.SubImage(box).(*ebiten.Image) // 体の円が枠からはみ出さないように切り取る
	vector.FillRect(clip, x, y, size, size, color.RGBA{0x55, 0x5b, 0x6b, 0xff}, false)
	fg := color.RGBA{0x9a, 0xa2, 0xb8, 0xff}
	vector.FillCircle(clip, x+size/2, y+size*0.38, size*0.2, fg, true)
	vector.FillCircle(clip, x+size/2, y+size*1.05, size*0.38, fg, true)
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
