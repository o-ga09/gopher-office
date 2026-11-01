package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

func TestGormStoreRoomsAndMembers(t *testing.T) {
	s := newGormStore(t)
	ctx := context.Background()

	owner, guest := newPlayer("owner"), newPlayer("guest")
	for _, p := range []*Player{owner, guest} {
		if err := s.CreatePlayer(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	code, _ := NewInviteCode()
	w := &Workspace{ID: ulid.Make().String(), OwnerID: owner.ID, Name: "W", InviteCode: code, MaxPlayers: 10, CreatedAt: now}
	r1 := &Room{ID: ulid.Make().String(), WorkspaceID: w.ID, Name: "Main", MapID: defaultMapID}
	if err := s.CreateWorkspace(ctx, w, r1); err != nil {
		t.Fatal(err)
	}
	r2 := &Room{ID: ulid.Make().String(), WorkspaceID: w.ID, Name: "Meeting", MapID: defaultMapID}
	if err := s.CreateRoom(ctx, r2); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountRooms(ctx, w.ID); err != nil || n != 2 {
		t.Fatalf("count = %d err=%v", n, err)
	}
	if rs, err := s.ListRooms(ctx, w.ID); err != nil || len(rs) != 2 || rs[0].ID != r1.ID || rs[1].ID != r2.ID {
		t.Fatalf("rooms = %+v err=%v", rs, err)
	}
	if got, err := s.GetRoom(ctx, w.ID, r2.ID); err != nil || got.Name != "Meeting" {
		t.Fatalf("room = %+v err=%v", got, err)
	}
	if _, err := s.GetRoom(ctx, ulid.Make().String(), r2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign workspace: err = %v", err)
	}

	m := &WorkspaceMember{WorkspaceID: w.ID, PlayerID: guest.ID, Role: roleMember, FirstJoinedAt: now, LastJoinedAt: now}
	if err := s.UpsertMember(ctx, m); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Hour)
	if err := s.UpsertMember(ctx, &WorkspaceMember{WorkspaceID: w.ID, PlayerID: guest.ID, Role: roleOwner, FirstJoinedAt: later, LastJoinedAt: later}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListMyWorkspaces(ctx, guest.ID, 50)
	if err != nil || len(got) != 1 {
		t.Fatalf("list = %+v err=%v", got, err)
	}
	if got[0].Workspace.ID != w.ID || got[0].Workspace.InviteCode != code || got[0].Role != roleMember || !got[0].LastJoinedAt.Equal(later) {
		t.Fatalf("a re-join must only bump last_joined_at: %+v", got[0])
	}
}
