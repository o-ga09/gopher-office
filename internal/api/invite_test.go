package api

import (
	"strings"
	"testing"
)

func TestNewInviteCode(t *testing.T) {
	seen := map[string]bool{}
	counts := map[byte]int{}
	const n = 5000
	for i := 0; i < n; i++ {
		code, err := NewInviteCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != InviteCodeLen || strings.Trim(code, base62) != "" {
			t.Fatalf("bad code %q", code)
		}
		if seen[code] {
			t.Fatalf("duplicate code %q after %d codes", code, i)
		}
		seen[code] = true
		for j := 0; j < len(code); j++ {
			counts[code[j]]++
		}
	}

	// 62 文字がどれもほぼ同じ頻度で出る（偏りがない）。期待値は n*12/62 ≒ 968、標準偏差は約 31
	if len(counts) != len(base62) {
		t.Fatalf("only %d of %d characters appeared", len(counts), len(base62))
	}
	want := float64(n*InviteCodeLen) / float64(len(base62))
	for c, got := range counts {
		if float64(got) < want*0.8 || float64(got) > want*1.2 {
			t.Errorf("character %q appeared %d times, expected about %.0f", c, got, want)
		}
	}
}
