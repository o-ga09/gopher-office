// Package blob はオブジェクトストレージの最小の抽象。アイコン画像など、内容でキーが決まる不変のデータを置く。
package blob

import (
	"context"
	"errors"
	"sync"
)

// ErrNotFound はキーのオブジェクトが無いことを表す。
var ErrNotFound = errors.New("blob: not found")

// Store はキーでバイト列を出し入れする。
type Store interface {
	// Put はオブジェクトを保存する。同じキーへの再実行は冪等（上書きしても同じ結果になる）。
	Put(ctx context.Context, key string, data []byte, contentType string) error
	// Get はオブジェクトを返す。無ければ ErrNotFound。
	Get(ctx context.Context, key string) ([]byte, error)
}

// Memory はテスト用のメモリ上の Store。
type Memory struct {
	mu sync.Mutex
	m  map[string][]byte
}

func NewMemory() *Memory { return &Memory{m: map[string][]byte{}} }

func (s *Memory) Put(_ context.Context, key string, data []byte, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = append([]byte(nil), data...)
	return nil
}

func (s *Memory) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.m[key]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), d...), nil
}

// Len は保存されているオブジェクトの数（テスト用）。
func (s *Memory) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}
