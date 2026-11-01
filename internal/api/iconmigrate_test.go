package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/o-ga09/gopher-office/internal/blob"
)

func TestMigrateIcons(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()
	const n = 250 // 3 ページにまたがる
	hashes := map[string][]byte{}
	for i := range n {
		data := []byte(strings.Repeat("x", i+1))
		sum := sha256.Sum256(data)
		h := hex.EncodeToString(sum[:])
		s.icons[h] = data
		hashes[h] = data
	}
	bad := strings.Repeat("0", 64) // 内容と合わないハッシュ
	s.icons[bad] = []byte("tampered")

	dst := blob.NewMemory()
	res, err := MigrateIcons(ctx, s, dst, true, nil)
	if err != nil || res.Put != n || res.Mismatch != 1 || res.Scanned != n+1 || dst.Len() != 0 {
		t.Fatalf("dry-run: %+v len=%d err=%v", res, dst.Len(), err)
	}

	for range 2 { // 冪等
		res, err = MigrateIcons(ctx, s, dst, false, nil)
		if err != nil || res.Put != n || res.Mismatch != 1 || dst.Len() != n {
			t.Fatalf("migrate: %+v len=%d err=%v", res, dst.Len(), err)
		}
	}
	for h, data := range hashes {
		got, err := dst.Get(ctx, IconBlobKey(h))
		if err != nil || string(got) != string(data) {
			t.Fatalf("blob %s: %v", h, err)
		}
	}
	if _, err := dst.Get(ctx, IconBlobKey(bad)); err == nil {
		t.Fatal("a mismatched icon must not be migrated")
	}
}
