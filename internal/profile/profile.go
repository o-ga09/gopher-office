// Package profile はプロフィール（表示名・性別）の正規化と検証。GameServer と API で共有する。
package profile

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxNameRunes は表示名の最大文字数（rune 数）。
	MaxNameRunes = 32
	// DefaultName は表示名が空になったときの名前。
	DefaultName = "Gopher"
)

// CleanName は表示名を正規化する。
// 文字種は制限しないが、描画の崩れやログの汚染、表示の乗っ取りにつながる制御文字・書式文字
// （改行、BiDi 制御、ゼロ幅文字など）は除去し、前後の空白を削って最大 MaxNameRunes 文字に切り詰める。
func CleanName(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if n >= MaxNameRunes {
			break
		}
		b.WriteRune(r)
		n++
	}
	name := strings.TrimFunc(b.String(), unicode.IsSpace)
	if name == "" {
		return DefaultName
	}
	return name
}
