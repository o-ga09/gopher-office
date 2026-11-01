package client

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/o-ga09/gopher-office/internal/apiclient"
)

func TestNormalizeEmail(t *testing.T) {
	if got := normalizeEmail("  a@example.com \t"); got != "a@example.com" {
		t.Errorf("got %q", got)
	}
}

func TestValidateEmail(t *testing.T) {
	for in, want := range map[string]string{
		"a@example.com": "", "a@b": "", "": "invalid_email", "abc": "invalid_email",
		"@b.com": "invalid_email", "a@": "invalid_email", "a b@c.com": "invalid_email",
		strings.Repeat("a", 250) + "@b.com": "invalid_email",
	} {
		if got := validateEmail(in); got != want {
			t.Errorf("validateEmail(%q)=%q want %q", in, got, want)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	for pw, want := range map[string]string{
		"1234567": "password_length", "12345678": "", strings.Repeat("a", 128): "",
		strings.Repeat("a", 129): "password_length",
	} {
		if got := validatePassword(pw); got != want {
			t.Errorf("validatePassword(len %d)=%q want %q", len(pw), got, want)
		}
	}
}

func TestAccountMessage(t *testing.T) {
	for code := range accountMessages {
		for _, l := range []lang{langEN, langJA} {
			if accountMessage(code, l) == "" {
				t.Errorf("empty message for %s/%d", code, l)
			}
		}
	}
	if accountMessage("invalid_credentials", langEN) == accountMessage("invalid_credentials", langJA) {
		t.Error("EN and JA should differ")
	}
	if accountMessage("zzz", langEN) == "" || accountMessage("zzz", langJA) == "" {
		t.Error("unknown code needs a generic message")
	}
}

func TestAccountErrorMessage(t *testing.T) {
	apiErr := fmt.Errorf("login: %w", &apiclient.Error{Status: 409, Code: "email_taken"})
	if got, want := accountErrorMessage(apiErr, langEN), accountMessage("email_taken", langEN); got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if got, want := accountErrorMessage(errors.New("dial"), langJA), accountMessage("unavailable", langJA); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestTextFieldMask(t *testing.T) {
	f := textField{max: 10, mask: true}
	f.set("abc")
	if f.String() != "abc" || f.display() != "•••" {
		t.Errorf("String=%q display=%q", f.String(), f.display())
	}
	f.mask = false
	if f.display() != "abc" {
		t.Errorf("display=%q", f.display())
	}
}

func TestTextFieldByteLimit(t *testing.T) {
	f := textField{max: maxPasswordLen, maxByte: maxPasswordLen}
	f.set(strings.Repeat("あ", 100)) // 3 バイト × 42 = 126 まで入る
	if got := len(f.String()); got != 126 {
		t.Fatalf("bytes = %d, want 126", got)
	}
	if validatePassword(f.String()) != "" {
		t.Fatal("a full field must pass validatePassword")
	}
	f.add('a')
	f.add('b')
	f.add('c') // 129 バイト目は入らない
	if got := len(f.String()); got != 128 {
		t.Fatalf("bytes = %d, want 128", got)
	}
	f.clear()
	f.set("x")
	if f.bytes != 1 {
		t.Fatalf("bytes after clear+set = %d", f.bytes)
	}
}
