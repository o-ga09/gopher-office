package api

import (
	"context"
)

func (s *memStore) CountWorkspacesByOwner(_ context.Context, ownerID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, w := range s.workspaces {
		if w.OwnerID == ownerID {
			n++
		}
	}
	return n, nil
}

func (s *memStore) CreateWorkspace(_ context.Context, w *Workspace, r *Room) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forceDuplicates > 0 {
		s.forceDuplicates--
		return ErrDuplicate
	}
	for _, o := range s.workspaces {
		if o.InviteCode == w.InviteCode {
			return ErrDuplicate
		}
	}
	cw, cr := *w, *r
	s.workspaces[w.ID], s.rooms[r.ID] = &cw, &cr
	return nil
}

func (s *memStore) GetWorkspaceByCode(_ context.Context, code string) (*Workspace, *Room, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.workspaces {
		if w.InviteCode != code {
			continue
		}
		if rs := s.roomsOf(w.ID); len(rs) > 0 {
			cw, cr := *w, rs[0]
			return &cw, &cr, nil
		}
	}
	return nil, nil, ErrNotFound
}
