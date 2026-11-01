package password

import (
	"errors"
	"strings"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected PHC: %s", h)
	}
	if ok, err := VerifyPassword("correct horse", h); err != nil || !ok {
		t.Fatalf("verify = %v, %v", ok, err)
	}
	if ok, err := VerifyPassword("wrong horse!", h); err != nil || ok {
		t.Fatalf("mismatch verify = %v, %v", ok, err)
	}
	h2, _ := HashPassword("correct horse")
	if h == h2 {
		t.Fatal("salt must differ")
	}
}

func TestPasswordLength(t *testing.T) {
	for _, pw := range []string{"", "1234567", strings.Repeat("a", 129)} {
		if _, err := HashPassword(pw); !errors.Is(err, ErrPasswordLength) {
			t.Errorf("len %d: err = %v", len(pw), err)
		}
	}
	for _, pw := range []string{"12345678", strings.Repeat("a", 128)} {
		if err := ValidatePassword(pw); err != nil {
			t.Errorf("len %d: %v", len(pw), err)
		}
	}
}

func TestVerifyBrokenPHC(t *testing.T) {
	good, _ := HashPassword("correct horse")
	bad := []string{
		"", "plain", "$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHQ$" + strings.Repeat("A", 43),
		strings.Replace(good, "v=19", "v=16", 1),
		strings.Replace(good, "m=19456", "m=99999999", 1), // 巨大なメモリ
		strings.Replace(good, "t=2", "t=0", 1),
		strings.Replace(good, ",p=1", ",p=0", 1),
		good + "$extra",
		"$argon2id$v=19$m=19456,t=2,p=1$!!!$!!!",
	}
	for _, b := range bad {
		if ok, err := VerifyPassword("correct horse", b); ok || !errors.Is(err, ErrInvalidHash) {
			t.Errorf("%q: ok=%v err=%v", b, ok, err)
		}
	}
}
