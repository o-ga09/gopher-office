package api

import (
	"context"
	"time"
)

// memAccounts は AccountStore のメモリ実装。token_version は memStore の Player に持つ。
type memAccounts struct {
	s       *memStore
	byID    map[string]*Credential
	byEmail map[string]*Credential
}

func newMemAccounts(s *memStore) *memAccounts {
	return &memAccounts{s: s, byID: map[string]*Credential{}, byEmail: map[string]*Credential{}}
}

func (m *memAccounts) CreateCredential(_ context.Context, c *Credential) error {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	if _, ok := m.s.players[c.PlayerID]; !ok {
		return ErrNotFound
	}
	if m.byID[c.PlayerID] != nil || m.byEmail[c.Email] != nil {
		return ErrDuplicate
	}
	cp := *c
	m.byID[c.PlayerID], m.byEmail[c.Email] = &cp, &cp
	return nil
}

func (m *memAccounts) GetCredential(_ context.Context, id string) (*Credential, error) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	c, ok := m.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (m *memAccounts) GetCredentialByEmail(_ context.Context, email string) (*Credential, error) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	c, ok := m.byEmail[email]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (m *memAccounts) ChangePassword(_ context.Context, id, newHash string) (uint32, error) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	c, ok := m.byID[id]
	if !ok {
		return 0, ErrNotFound
	}
	c.PasswordHash, c.UpdatedAt = newHash, time.Now().UTC()
	m.s.players[id].TokenVersion++
	return m.s.players[id].TokenVersion, nil
}
