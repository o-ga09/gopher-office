package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	playerSecret = "player-secret-0123456789-0123456789"
	roomSecret   = "room-secret-0123456789-0123456789-x"
)

func newTestSigner(t *testing.T) (*Signer, *time.Time) {
	t.Helper()
	s, err := NewSigner(playerSecret, roomSecret)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 6, 19, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	return s, &now
}

func TestNewSignerValidatesSecrets(t *testing.T) {
	if _, err := NewSigner("short", roomSecret); err == nil {
		t.Error("short player secret must be rejected")
	}
	if _, err := NewSigner(playerSecret, "short"); err == nil {
		t.Error("short room secret must be rejected")
	}
	if _, err := NewSigner(playerSecret, playerSecret); err == nil {
		t.Error("identical secrets must be rejected")
	}
}

func TestPlayerTokenRoundTripAndExpiry(t *testing.T) {
	s, now := newTestSigner(t)
	tok, err := s.IssuePlayerToken("player-1")
	if err != nil {
		t.Fatal(err)
	}
	if id, err := s.ParsePlayerToken(tok); err != nil || id != "player-1" {
		t.Fatalf("id=%q err=%v", id, err)
	}

	*now = now.Add(PlayerTokenTTL - time.Minute)
	if _, err := s.ParsePlayerToken(tok); err != nil {
		t.Fatalf("must still be valid just before expiry: %v", err)
	}
	*now = now.Add(time.Hour)
	if _, err := s.ParsePlayerToken(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired token must be rejected, got %v", err)
	}
}

func TestRoomTokenRoundTripAndShortLife(t *testing.T) {
	s, now := newTestSigner(t)
	tok, exp, err := s.IssueRoomToken(RoomClaims{PlayerID: "p", RoomID: "room-1", GameServer: "gs-1"})
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(RoomTokenTTL); !exp.Equal(want) {
		t.Fatalf("expiresAt = %v, want %v", exp, want)
	}
	c, err := s.ParseRoomToken(tok)
	if err != nil || c.PlayerID != "p" || c.RoomID != "room-1" || c.GameServer != "gs-1" {
		t.Fatalf("claims=%+v err=%v", c, err)
	}

	*now = now.Add(RoomTokenTTL + 10*time.Second)
	if _, err := s.ParseRoomToken(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("a room token must expire within a minute, got %v", err)
	}
}

func TestTokensCannotBeUsedForAnotherPurpose(t *testing.T) {
	s, _ := newTestSigner(t)
	player, _ := s.IssuePlayerToken("p")
	room, _, _ := s.IssueRoomToken(RoomClaims{PlayerID: "p", RoomID: "r"})

	if _, err := s.ParseRoomToken(player); !errors.Is(err, ErrInvalidToken) {
		t.Error("a player token must not be accepted as a room token")
	}
	if _, err := s.ParsePlayerToken(room); !errors.Is(err, ErrInvalidToken) {
		t.Error("a room token must not be accepted as a player token")
	}
}

func TestForgedAndMalformedTokensAreRejected(t *testing.T) {
	s, now := newTestSigner(t)
	good, _ := s.IssuePlayerToken("p")

	// 別の鍵で署名
	other, _ := NewSigner("another-player-secret-0123456789-abc", roomSecret)
	other.now = s.now
	forged, _ := other.IssuePlayerToken("p")

	// alg=none
	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Issuer: issuer, Subject: "p", Audience: jwt.ClaimStrings{audiencePlayer},
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)

	// 有効期限なし
	noExp, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer: issuer, Subject: "p", Audience: jwt.ClaimStrings{audiencePlayer},
	}).SignedString([]byte(playerSecret))

	// 発行者が違う
	badIss, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer: "someone-else", Subject: "p", Audience: jwt.ClaimStrings{audiencePlayer},
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}).SignedString([]byte(playerSecret))

	// subject なし
	noSub, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer: issuer, Audience: jwt.ClaimStrings{audiencePlayer},
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}).SignedString([]byte(playerSecret))

	cases := map[string]string{
		"wrong key":      forged,
		"alg none":       none,
		"no expiry":      noExp,
		"wrong issuer":   badIss,
		"no subject":     noSub,
		"tampered":       good[:len(good)-2] + "xx",
		"empty":          "",
		"garbage":        "not-a-jwt",
		"truncated":      strings.Split(good, ".")[0],
		"extra segments": good + ".extra",
	}
	for name, tok := range cases {
		if _, err := s.ParsePlayerToken(tok); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s: want ErrInvalidToken, got %v", name, err)
		}
	}
}

func TestRoomVerifier(t *testing.T) {
	s, now := newTestSigner(t)
	tok, _, _ := s.IssueRoomToken(RoomClaims{PlayerID: "player-7", RoomID: "room-1", GameServer: "gs"})

	room := "room-1"
	v, err := NewRoomVerifier(roomSecret, func() string { return room })
	if err != nil {
		t.Fatal(err)
	}
	v.now = s.now

	if id, err := v.Verify(tok); err != nil || id != "player-7" {
		t.Fatalf("id=%q err=%v", id, err)
	}

	room = "room-2" // 別のルームを担当する GameServer では使えない
	if _, err := v.Verify(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("a token for another room must be rejected, got %v", err)
	}

	room = "" // 担当ルームが未確定（ローカル開発）なら、どのルームでも通る
	if _, err := v.Verify(tok); err != nil {
		t.Fatalf("unbound verifier must accept: %v", err)
	}

	// 検証側は鍵が違えば通らない／期限切れは通らない
	bad, _ := NewRoomVerifier("a-different-room-secret-0123456789-xyz", nil)
	bad.now = s.now
	if _, err := bad.Verify(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("a verifier with another key must reject")
	}
	*now = now.Add(time.Hour)
	if _, err := v.Verify(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("expired token must be rejected")
	}

	// GameServer 側の検証鍵では playerToken は検証できない
	player, _ := s.IssuePlayerToken("p")
	*now = now.Add(-time.Hour)
	if _, err := v.Verify(player); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("a verifier must not accept player tokens")
	}

	if _, err := NewRoomVerifier("short", nil); err == nil {
		t.Fatal("a short secret must be rejected")
	}
}

func TestPlayerTokenVersion(t *testing.T) {
	s, err := NewSigner(strings.Repeat("p", 32), strings.Repeat("r", 32))
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := s.IssuePlayerTokenV("p1", 7)
	if c, err := s.ParsePlayerTokenClaims(tok); err != nil || c.PlayerID != "p1" || c.Version != 7 {
		t.Fatalf("claims = %+v, %v", c, err)
	}
	if id, err := s.ParsePlayerToken(tok); err != nil || id != "p1" {
		t.Fatalf("ParsePlayerToken = %q, %v", id, err)
	}
	old, _ := s.IssuePlayerToken("p1")
	if c, err := s.ParsePlayerTokenClaims(old); err != nil || c.Version != 0 {
		t.Fatalf("legacy claims = %+v, %v", c, err)
	}
}
