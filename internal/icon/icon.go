// Package icon はプロフィールアイコン画像の検証と再エンコード（docs/design.md §8）。
package icon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/draw"
	_ "image/jpeg" // image.Decode に JPEG を登録する
	"image/png"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // image.Decode に WebP を登録する
)

const (
	// MaxUploadBytes はアップロードできる画像のサイズ上限。
	MaxUploadBytes = 256 << 10
	// MaxDimension は元画像の幅・高さの上限（decompression bomb 対策）。
	MaxDimension = 4096
	// Size は保存・配信するアイコンの一辺（px）。
	Size = 64
	// HashLen は Hash の長さ（SHA-256 の 16 進数）。
	HashLen = 64
)

var (
	// ErrTooLarge は元画像のバイト数が上限を超えたことを表す。
	ErrTooLarge = errors.New("icon: image is too large")
	// ErrUnsupported は PNG / JPEG / WebP のどれでもない（または壊れている）ことを表す。
	ErrUnsupported = errors.New("icon: unsupported or corrupt image")
	// ErrDimensions は元画像の寸法が上限を超えたことを表す。
	ErrDimensions = errors.New("icon: image dimensions are too large")
)

// Process は画像を検証し、Size×Size の PNG に再エンコードして返す。hash はその PNG の SHA-256（16 進数）。
//
// 形式は Content-Type ではなく中身から判定し、PNG / JPEG / WebP だけを許可する。デコードの前に
// 寸法を確認し、巨大な画像でメモリを使い切られないようにする。再エンコードで EXIF などは残らない。
// 正方形でない画像は、中央を正方形に切り出してから縮小する。
func Process(data []byte) (pngData []byte, hash string, err error) {
	if len(data) > MaxUploadBytes {
		return nil, "", ErrTooLarge
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg" && format != "webp") {
		return nil, "", ErrUnsupported
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, "", ErrUnsupported
	}
	if cfg.Width > MaxDimension || cfg.Height > MaxDimension {
		return nil, "", ErrDimensions
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", ErrUnsupported
	}

	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	x0, y0 := b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2
	crop := image.Rect(x0, y0, x0+side, y0+side)

	dst := image.NewNRGBA(image.Rect(0, 0, Size, Size))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, draw.Src, nil)

	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, dst); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:]), nil
}

// ValidHash は s が Process の返す形式（小文字 16 進数 64 文字）かを返す。
func ValidHash(s string) bool {
	if len(s) != HashLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
