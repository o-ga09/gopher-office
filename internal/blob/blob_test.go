package blob

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func stores(t *testing.T) map[string]Store {
	t.Helper()
	d, err := NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return map[string]Store{"memory": NewMemory(), "dir": d}
}

func TestStoreContract(t *testing.T) {
	ctx := context.Background()
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Get(ctx, "icons/none.png"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing: err = %v", err)
			}
			if err := s.Put(ctx, "icons/a.png", []byte("one"), "image/png"); err != nil {
				t.Fatal(err)
			}
			if err := s.Put(ctx, "icons/a.png", []byte("one"), "image/png"); err != nil {
				t.Fatalf("Put must be idempotent: %v", err)
			}
			got, err := s.Get(ctx, "icons/a.png")
			if err != nil || !bytes.Equal(got, []byte("one")) {
				t.Fatalf("Get = %q, %v", got, err)
			}
			got[0] = 'X' // 返した値を書き換えても保存内容に影響しない
			if again, _ := s.Get(ctx, "icons/a.png"); string(again) != "one" {
				t.Fatalf("stored data mutated: %q", again)
			}
		})
	}
}

func TestDirRejectsUnsafeKeys(t *testing.T) {
	root := t.TempDir()
	d, err := NewDir(filepath.Join(root, "store"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, k := range []string{"../evil", "a/../../evil", "/abs", "", "a//b", "UPPER", "a b", "..", "icons/..", "a\\b", "x/./y"} {
		if err := d.Put(ctx, k, []byte("x"), ""); err == nil {
			t.Errorf("Put(%q) must fail", k)
		}
		if _, err := d.Get(ctx, k); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) must be rejected, got %v", k, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "evil")); err == nil {
		t.Fatal("file escaped the root")
	}
}
