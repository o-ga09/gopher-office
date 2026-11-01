package api

import (
	"context"
)

func (s *memStore) CreatePlayer(_ context.Context, p *Player) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failCreate != nil {
		return s.failCreate
	}
	cp := *p
	s.players[p.ID] = &cp
	return nil
}

func (s *memStore) GetPlayer(_ context.Context, id string) (*Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.players[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (s *memStore) UpdatePlayer(_ context.Context, id string, u PlayerUpdate) (*Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.players[id]
	if !ok {
		return nil, ErrNotFound
	}
	if u.DisplayName != nil {
		p.DisplayName = *u.DisplayName
	}
	if u.Gender != nil {
		p.Gender = *u.Gender
	}
	cp := *p
	return &cp, nil
}
