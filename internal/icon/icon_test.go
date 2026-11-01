package icon

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func solid(w, h int, c color.Color) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestProcess_PNGは64x64のPNGになる(t *testing.T) {
	out, hash, err := Process(encodePNG(t, solid(300, 120, color.NRGBA{200, 30, 30, 255})))
	if err != nil {
		t.Fatal(err)
	}
	got, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if b := got.Bounds(); b.Dx() != Size || b.Dy() != Size {
		t.Fatalf("size = %v", b)
	}
	if !ValidHash(hash) {
		t.Fatalf("hash = %q", hash)
	}
	if r, _, _, _ := got.At(32, 32).RGBA(); r>>8 < 190 {
		t.Fatalf("色が保たれていない: r=%d", r>>8)
	}
}

func TestProcess_JPEGも受け付ける(t *testing.T) {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, solid(80, 80, color.NRGBA{10, 10, 200, 255}), nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Process(b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func TestProcess_同じ入力は同じハッシュ(t *testing.T) {
	in := encodePNG(t, solid(100, 100, color.NRGBA{1, 2, 3, 255}))
	_, h1, _ := Process(in)
	_, h2, _ := Process(in)
	if h1 != h2 {
		t.Fatal("ハッシュが一致しない")
	}
}

func TestProcess_拒否する入力(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want error
	}{
		{"画像でない", []byte("GIF89a not really"), ErrUnsupported},
		{"空", nil, ErrUnsupported},
		{"壊れたPNG", encodePNG(t, solid(10, 10, color.White))[:30], ErrUnsupported},
		{"寸法が大きすぎる", encodePNG(t, image.NewGray(image.Rect(0, 0, MaxDimension+1, 1))), ErrDimensions},
		{"バイト数が大きすぎる", make([]byte, MaxUploadBytes+1), ErrTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := Process(tt.data); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestValidHash(t *testing.T) {
	ok := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for s, want := range map[string]bool{ok: true, "": false, ok[:63]: false, ok + "0": false, "G" + ok[1:]: false, "A" + ok[1:]: false} {
		if ValidHash(s) != want {
			t.Errorf("ValidHash(%q) = %v", s, !want)
		}
	}
}
