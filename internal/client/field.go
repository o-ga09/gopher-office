package client

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

// textField は 1 行のテキスト入力。フォーカスがあるときだけ、キー入力を受け取る。
type textField struct {
	text    []rune
	max     int // 最大の文字数（rune）
	maxByte int // 0 でなければ、UTF-8 でのバイト数の上限（サーバはパスワードをバイトで数える）
	bytes   int // text の UTF-8 でのバイト数
	focused bool
	mask    bool // true なら、表示は • にする（パスワード欄）
	frames  int  // カーソルの点滅用
}

func (f *textField) String() string { return string(f.text) }

// display は画面に出す文字列。mask のときは、文字数分の • になる。
func (f *textField) display() string {
	if !f.mask {
		return string(f.text)
	}
	return strings.Repeat("•", len(f.text))
}

func (f *textField) clear() { f.text = f.text[:0]; f.bytes = 0 }

func (f *textField) set(s string) {
	f.clear()
	for _, r := range s {
		f.add(r)
	}
}

func (f *textField) add(r rune) {
	if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || len(f.text) >= f.max {
		return
	}
	if f.maxByte > 0 && f.bytes+utf8.RuneLen(r) > f.maxByte {
		return
	}
	f.text = append(f.text, r)
	f.bytes += utf8.RuneLen(r)
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
		f.bytes -= utf8.RuneLen(f.text[len(f.text)-1])
		f.text = f.text[:len(f.text)-1]
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEnter) || inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		f.focused = false
	}
}

func (f *textField) caretVisible() bool { return f.focused && (f.frames/30)%2 == 0 }
