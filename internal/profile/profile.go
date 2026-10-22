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

// CleanName は表示名を正規化する。空になる場合は DefaultName を返す。
// 文字種は制限しないが、制御文字・書式文字は除去し、最大 MaxNameRunes 文字に切り詰める（CleanText 参照）。
func CleanName(s string) string {
	if name := CleanText(s, MaxNameRunes); name != "" {
		return name
	}
	return DefaultName
}

// CleanText は利用者が入力した表示用の文字列を正規化する。
// 文字種は制限しないが、描画の崩れやログの汚染、表示の乗っ取りにつながる制御文字・書式文字
// （改行、BiDi 制御、ゼロ幅文字など）は除去し、前後の空白を削って最大 maxRunes 文字に切り詰める。
// 結果が空になることがある。
func CleanText(s string, maxRunes int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	// 前後の空白を先に削ってから切り詰める（先頭の空白が文字数を消費しないように）。切り詰めの結果、末尾に空白が残ることがある
	s = strings.TrimFunc(b.String(), unicode.IsSpace)
	if utf8.RuneCountInString(s) > maxRunes {
		s = string([]rune(s)[:maxRunes])
	}
	return strings.TrimRightFunc(s, unicode.IsSpace)
}
