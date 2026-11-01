package profile

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanName(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain", "taichi", "taichi"},
		{"japanese and emoji are allowed", "たいち🐹", "たいち🐹"},
		{"trims spaces", "  gopher  ", "gopher"},
		{"empty falls back", "", DefaultName},
		{"only spaces falls back", "   ", DefaultName},
		{"strips newline and tab", "a\nb\tc", "abc"},
		{"strips NUL", "a\x00b", "ab"},
		{"strips BiDi override", "abc\u202edef", "abcdef"},
		{"strips zero width space", "a\u200bb", "ab"},
		{"invalid utf-8 is dropped", "a\xffb", "ab"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CleanName(c.in); got != c.want {
				t.Errorf("CleanName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestCleanNameTruncatesByRune(t *testing.T) {
	in := strings.Repeat("あ", MaxNameRunes+10)
	got := CleanName(in)
	if n := utf8.RuneCountInString(got); n != MaxNameRunes {
		t.Fatalf("rune count = %d, want %d", n, MaxNameRunes)
	}
	if !utf8.ValidString(got) {
		t.Fatal("result must be valid UTF-8")
	}
}
