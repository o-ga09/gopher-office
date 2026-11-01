package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	// MinPasswordLen と MaxPasswordLen はパスワードの長さ（バイト）の範囲。
	// 上限は、巨大な入力でハッシュ計算（と入力の保持）が重くなるのを防ぐ。
	MinPasswordLen = 8
	MaxPasswordLen = 128

	// argon2id の既定パラメータ（OWASP の推奨: m=19MiB, t=2, p=1）。
	argonMemoryKiB = 19 * 1024
	argonTime      = 2
	argonThreads   = 1
	argonKeyLen    = 32
	argonSaltLen   = 16

	// DB から読んだハッシュのパラメータの上限。改ざんされた値で、巨大なメモリを確保させないため。
	maxArgonMemoryKiB = 256 * 1024
	maxArgonTime      = 10
	maxArgonThreads   = 16
)

var (
	// ErrPasswordLength はパスワードの長さが範囲外なことを表す。
	ErrPasswordLength = fmt.Errorf("password: password must be %d-%d bytes", MinPasswordLen, MaxPasswordLen)
	// ErrInvalidHash はパスワードのハッシュ（PHC 形式）が壊れていることを表す。
	ErrInvalidHash = errors.New("password: invalid password hash")
)

var b64 = base64.RawStdEncoding

// ValidatePassword はパスワードの長さを検査する。
func ValidatePassword(pw string) error {
	if len(pw) < MinPasswordLen || len(pw) > MaxPasswordLen {
		return ErrPasswordLength
	}
	return nil
}

// HashPassword はパスワードを argon2id でハッシュし、PHC 形式の文字列を返す。
func HashPassword(pw string) (string, error) {
	if err := ValidatePassword(pw); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

type phc struct {
	memory, time uint32
	threads      uint8
	salt, key    []byte
}

func parsePHC(s string) (phc, error) {
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, key
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return phc{}, ErrInvalidHash
	}
	v, ok := strings.CutPrefix(parts[2], "v=")
	if n, err := strconv.Atoi(v); !ok || err != nil || n != argon2.Version {
		return phc{}, ErrInvalidHash
	}
	var h phc
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return phc{}, ErrInvalidHash
	}
	vals := [3]uint64{}
	for i, key := range []string{"m=", "t=", "p="} {
		raw, ok := strings.CutPrefix(params[i], key)
		if !ok {
			return phc{}, ErrInvalidHash
		}
		n, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			return phc{}, ErrInvalidHash
		}
		vals[i] = n
	}
	if vals[0] < 8 || vals[0] > maxArgonMemoryKiB || vals[1] < 1 || vals[1] > maxArgonTime || vals[2] < 1 || vals[2] > maxArgonThreads {
		return phc{}, ErrInvalidHash
	}
	h.memory, h.time, h.threads = uint32(vals[0]), uint32(vals[1]), uint8(vals[2])
	var err error
	if h.salt, err = b64.DecodeString(parts[4]); err != nil || len(h.salt) < 8 {
		return phc{}, ErrInvalidHash
	}
	if h.key, err = b64.DecodeString(parts[5]); err != nil || len(h.key) < 16 || len(h.key) > 64 {
		return phc{}, ErrInvalidHash
	}
	return h, nil
}

// VerifyPassword はパスワードがハッシュと一致するかを、定数時間で比べる。
// ハッシュが壊れていれば ErrInvalidHash を返す。長さの範囲外のパスワードは不一致として扱う。
func VerifyPassword(pw, encoded string) (bool, error) {
	h, err := parsePHC(encoded)
	if err != nil {
		return false, err
	}
	if len(pw) > MaxPasswordLen {
		return false, nil
	}
	got := argon2.IDKey([]byte(pw), h.salt, h.time, h.memory, h.threads, uint32(len(h.key)))
	return subtle.ConstantTimeCompare(got, h.key) == 1, nil
}
