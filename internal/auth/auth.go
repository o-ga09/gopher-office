// Package auth は API と GameServer が使うトークン（HS256 の JWT）の発行と検証。
//
// 鍵は用途ごとに分ける。GameServer が持つのは roomToken の検証鍵だけで、
// 漏れても playerToken（API の認証）は偽造できない。トークンの用途は aud でも区別し、
// playerToken を roomToken として使い回せないようにする。
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	issuer         = "gopher-office"
	audiencePlayer = "player"
	audienceRoom   = "room"

	// PlayerTokenTTL は playerToken の有効期間。
	PlayerTokenTTL = 30 * 24 * time.Hour
	// RoomTokenTTL は roomToken の有効期間。入室の直前にしか使わないので短い。
	RoomTokenTTL = 60 * time.Second
	// MinSecretLen は署名鍵の最小の長さ（バイト）。
	MinSecretLen = 32

	clockLeeway = 2 * time.Second
)

// ErrInvalidToken はトークンが不正（署名・期限・用途・形式）なことを表す。
var ErrInvalidToken = errors.New("auth: invalid token")

// RoomClaims は roomToken の中身。
type RoomClaims struct {
	PlayerID   string
	RoomID     string
	GameServer string
	ExpiresAt  time.Time
}

type roomClaims struct {
	Room string `json:"room"`
	GS   string `json:"gs"`
	jwt.RegisteredClaims
}

// Signer は API が使う、トークンの発行と検証。
type Signer struct {
	playerKey []byte
	roomKey   []byte
	now       func() time.Time
}

// NewSigner は鍵から Signer を作る。鍵は MinSecretLen 以上で、2 つは別の値でなければならない。
func NewSigner(playerSecret, roomSecret string) (*Signer, error) {
	if err := checkSecret("player", playerSecret); err != nil {
		return nil, err
	}
	if err := checkSecret("room", roomSecret); err != nil {
		return nil, err
	}
	if playerSecret == roomSecret {
		return nil, errors.New("auth: player and room secrets must differ")
	}
	return &Signer{playerKey: []byte(playerSecret), roomKey: []byte(roomSecret), now: time.Now}, nil
}

func checkSecret(name, s string) error {
	if len(s) < MinSecretLen {
		return fmt.Errorf("auth: %s secret must be at least %d bytes", name, MinSecretLen)
	}
	return nil
}

func (s *Signer) parser(audience string) *jwt.Parser {
	return jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(s.now),
		jwt.WithLeeway(clockLeeway),
	)
}

// PlayerClaims は playerToken の中身。
type PlayerClaims struct {
	PlayerID string
	// Version は token_version。パスワード変更で上がり、古いトークンを失効させる。ver の無いトークンは 0。
	Version uint32
}

type playerClaims struct {
	Ver uint32 `json:"ver,omitempty"`
	jwt.RegisteredClaims
}

// IssuePlayerToken は API の認証に使う playerToken（version 0）を発行する。
func (s *Signer) IssuePlayerToken(playerID string) (string, error) {
	return s.IssuePlayerTokenV(playerID, 0)
}

// IssuePlayerTokenV は token_version つきの playerToken を発行する。
func (s *Signer) IssuePlayerTokenV(playerID string, ver uint32) (string, error) {
	now := s.now()
	return jwt.NewWithClaims(jwt.SigningMethodHS256, playerClaims{
		Ver: ver,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   playerID,
			Audience:  jwt.ClaimStrings{audiencePlayer},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(PlayerTokenTTL)),
		},
	}).SignedString(s.playerKey)
}

// ParsePlayerToken は playerToken を検証し、プレイヤー ID を返す（version は見ない）。
func (s *Signer) ParsePlayerToken(token string) (string, error) {
	c, err := s.ParsePlayerTokenClaims(token)
	return c.PlayerID, err
}

// ParsePlayerTokenClaims は playerToken を検証し、プレイヤー ID と version を返す。
func (s *Signer) ParsePlayerTokenClaims(token string) (PlayerClaims, error) {
	var claims playerClaims
	if _, err := s.parser(audiencePlayer).ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) { return s.playerKey, nil }); err != nil {
		return PlayerClaims{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	if claims.Subject == "" {
		return PlayerClaims{}, fmt.Errorf("%w: no subject", ErrInvalidToken)
	}
	return PlayerClaims{PlayerID: claims.Subject, Version: claims.Ver}, nil
}

// IssueRoomToken は GameServer への入室に使う roomToken を発行する。
func (s *Signer) IssueRoomToken(c RoomClaims) (token string, expiresAt time.Time, err error) {
	now := s.now()
	expiresAt = now.Add(RoomTokenTTL)
	token, err = jwt.NewWithClaims(jwt.SigningMethodHS256, roomClaims{
		Room: c.RoomID,
		GS:   c.GameServer,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   c.PlayerID,
			Audience:  jwt.ClaimStrings{audienceRoom},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}).SignedString(s.roomKey)
	return token, expiresAt, err
}

// ParseRoomToken は roomToken を検証する（API のテストや、鍵を持つ側から使う）。
func (s *Signer) ParseRoomToken(token string) (RoomClaims, error) {
	return parseRoomToken(s.roomKey, s.now, token)
}

func parseRoomToken(key []byte, now func() time.Time, token string) (RoomClaims, error) {
	p := (&Signer{now: now}).parser(audienceRoom)
	var claims roomClaims
	if _, err := p.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) { return key, nil }); err != nil {
		return RoomClaims{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	if claims.Subject == "" || claims.Room == "" {
		return RoomClaims{}, fmt.Errorf("%w: missing claims", ErrInvalidToken)
	}
	return RoomClaims{
		PlayerID:   claims.Subject,
		RoomID:     claims.Room,
		GameServer: claims.GS,
		ExpiresAt:  claims.ExpiresAt.Time,
	}, nil
}

// RoomVerifier は GameServer が使う roomToken の検証。gameserver.TokenVerifier を満たす。
// 検証鍵だけを持ち、トークンを発行することも、playerToken を検証することもできない。
type RoomVerifier struct {
	key []byte
	// RoomID は、この GameServer が担当するルームの ID を返す。空を返す間は、どのルームのトークンも受け付ける
	// （ローカル開発用。Agones では割り当て時に決まる annotation から返す）。
	RoomID func() string
	now    func() time.Time
}

// NewRoomVerifier は roomToken の検証鍵から RoomVerifier を作る。
func NewRoomVerifier(roomSecret string, roomID func() string) (*RoomVerifier, error) {
	if err := checkSecret("room", roomSecret); err != nil {
		return nil, err
	}
	return &RoomVerifier{key: []byte(roomSecret), RoomID: roomID, now: time.Now}, nil
}

// Claims は roomToken を検証し、中身を返す（ゲートウェイが、接続先の GameServer を知るために使う）。
// ルームの一致は確認しない（ゲートウェイはルームを担当しない。GameServer が確認する）。
func (v *RoomVerifier) Claims(token string) (RoomClaims, error) {
	return parseRoomToken(v.key, v.now, token)
}

// Verify は roomToken を検証し、プレイヤー ID を返す。
func (v *RoomVerifier) Verify(token string) (string, error) {
	c, err := parseRoomToken(v.key, v.now, token)
	if err != nil {
		return "", err
	}
	if v.RoomID != nil {
		if want := v.RoomID(); want != "" && c.RoomID != want {
			return "", fmt.Errorf("%w: token is for another room", ErrInvalidToken)
		}
	}
	return c.PlayerID, nil
}
