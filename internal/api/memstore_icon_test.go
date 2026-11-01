package api

import (
	"context"
	"sort"
)

func (s *memStore) SetIcon(_ context.Context, playerID, hash string, data []byte) (*Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.players[playerID]
	if !ok {
		return nil, ErrNotFound
	}
	s.icons[hash] = append([]byte(nil), data...)
	p.IconHash = &hash
	cp := *p
	return &cp, nil
}

func (s *memStore) GetIconHash(_ context.Context, playerID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.players[playerID]
	if !ok || p.IconHash == nil {
		return "", ErrNotFound
	}
	return *p.IconHash, nil
}

func (s *memStore) LegacyIconData(_ context.Context, hash string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.icons[hash]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), d...), nil
}

func (s *memStore) ListIcons(_ context.Context, afterHash string, limit int) ([]PlayerIcon, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var hs []string
	for h := range s.icons {
		if h > afterHash {
			hs = append(hs, h)
		}
	}
	sort.Strings(hs)
	if len(hs) > limit {
		hs = hs[:limit]
	}
	out := make([]PlayerIcon, 0, len(hs))
	for _, h := range hs {
		out = append(out, PlayerIcon{Hash: h, Data: append([]byte(nil), s.icons[h]...)})
	}
	return out, nil
}
