package blob

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var validKey = regexp.MustCompile(`^[a-z0-9/._-]+$`)

// Dir はローカルのディレクトリに置く Store（開発用）。
type Dir struct{ root string }

func NewDir(root string) (*Dir, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	return &Dir{root: root}, nil
}

func (d *Dir) path(key string) (string, error) {
	if !validKey.MatchString(key) || strings.HasPrefix(key, "/") {
		return "", fmt.Errorf("blob: invalid key %q", key)
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.Contains(seg, "..") {
			return "", fmt.Errorf("blob: invalid key %q", key)
		}
	}
	return filepath.Join(d.root, filepath.FromSlash(key)), nil
}

func (d *Dir) Put(_ context.Context, key string, data []byte, _ string) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // rename 済みなら失敗するだけ
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func (d *Dir) Get(_ context.Context, key string) ([]byte, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p) //nolint:gosec // p は d.path がキー検査済み
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return data, err
}
