package api

import (
	"context"
	"sort"
)

func (s *memStore) ListRooms(_ context.Context, workspaceID string) ([]Room, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.roomsOf(workspaceID), nil
}

// roomsOf は mu を持った状態で呼ぶ。
func (s *memStore) roomsOf(workspaceID string) []Room {
	var rs []Room
	for _, r := range s.rooms {
		if r.WorkspaceID == workspaceID {
			rs = append(rs, *r)
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
	return rs
}

func (s *memStore) GetRoom(_ context.Context, workspaceID, roomID string) (*Room, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.rooms[roomID]; ok && r.WorkspaceID == workspaceID {
		c := *r
		return &c, nil
	}
	return nil, ErrNotFound
}

func (s *memStore) CountRooms(_ context.Context, workspaceID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int64(len(s.roomsOf(workspaceID))), nil
}

func (s *memStore) CreateRoom(_ context.Context, r *Room) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *r
	s.rooms[r.ID] = &c
	return nil
}

func (s *memStore) UpsertMember(_ context.Context, m *WorkspaceMember) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failMember != nil {
		return s.failMember
	}
	if s.members == nil {
		s.members = map[[2]string]*WorkspaceMember{}
	}
	k := [2]string{m.WorkspaceID, m.PlayerID}
	if old, ok := s.members[k]; ok {
		old.LastJoinedAt = m.LastJoinedAt
		return nil
	}
	c := *m
	s.members[k] = &c
	return nil
}

func (s *memStore) ListMyWorkspaces(_ context.Context, playerID string, limit int) ([]MyWorkspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []MyWorkspace
	for k, m := range s.members {
		if k[1] != playerID {
			continue
		}
		if w, ok := s.workspaces[m.WorkspaceID]; ok {
			out = append(out, MyWorkspace{Workspace: *w, Role: m.Role, LastJoinedAt: m.LastJoinedAt})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastJoinedAt.After(out[j].LastJoinedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
