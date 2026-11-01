package api

import (
	"crypto/rand"
	"fmt"
)

const (
	// InviteCodeLen は招待コードの長さ。base62 の 12 文字で約 71 ビット。推測して当てることはできない。
	InviteCodeLen = 12
	base62        = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// NewInviteCode は暗号論的乱数で招待コードを作る。
func NewInviteCode() (string, error) {
	out := make([]byte, 0, InviteCodeLen)
	buf := make([]byte, InviteCodeLen*2)
	// 256 を 62 で割った余りぶんの偏りが出ないよう、248（= 62 * 4）以上のバイトは捨てる
	const limit = 256 - 256%len(base62)
	for len(out) < InviteCodeLen {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("api: random: %w", err)
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, base62[int(b)%len(base62)])
			if len(out) == InviteCodeLen {
				break
			}
		}
	}
	return string(out), nil
}
