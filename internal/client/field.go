package client

import (
	"unicode"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

// textField は 1 行のテキスト入力。フォーカスがあるときだけ、キー入力を受け取る。
type textField struct {
	text    []rune
	max     int
	focused bool
	frames  int // カーソルの点滅用
}

func (f *textField) String() string { return string(f.text) }

func (f *textField) set(s string) {
	f.text = f.text[:0]
	for _, r := range s {
		f.add(r)
	}
}

func (f *textField) add(r rune) {
	if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || len(f.text) >= f.max {
		return
	}
	f.text = append(f.text, r)
}

// update はフォーカスがあるとき、入力された文字と Backspace を反映する。Enter / Escape でフォーカスを外す。
func (f *textField) update() {
	f.frames++
	if !f.focused {
		return
	}
	for _, r := range ebiten.AppendInputChars(nil) {
		f.add(r)
	}
	// 押しっぱなしでは、0.5 秒後から繰り返して消す
	if d := inpututil.KeyPressDuration(ebiten.KeyBackspace); len(f.text) > 0 && (d == 1 || (d > 30 && d%3 == 0)) {
		f.text = f.text[:len(f.text)-1]
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEnter) || inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		f.focused = false
	}
}

func (f *textField) caretVisible() bool { return f.focused && (f.frames/30)%2 == 0 }
