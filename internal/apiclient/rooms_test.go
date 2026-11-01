package apiclient_test

import (
	"context"
	"testing"

	"github.com/o-ga09/gopher-office/internal/apiclient"
)

func TestClientRooms(t *testing.T) {
	ctx := context.Background()
	base := startServer(t)
	owner := apiclient.New(base)
	if _, err := owner.CreateGuest(ctx, "owner", 0); err != nil {
		t.Fatal(err)
	}
	ws, err := owner.CreateWorkspace(ctx, "Main")
	if err != nil {
		t.Fatal(err)
	}
	meeting, err := owner.CreateRoom(ctx, ws.InviteCode, "Meeting")
	if err != nil || meeting.ID == "" || meeting.Name != "Meeting" {
		t.Fatalf("room=%+v err=%v", meeting, err)
	}
	rooms, err := owner.Rooms(ctx, ws.InviteCode)
	if err != nil || len(rooms) != 2 || rooms[1] != meeting {
		t.Fatalf("rooms=%+v err=%v", rooms, err)
	}

	join, err := owner.JoinRoom(ctx, ws.InviteCode, meeting.ID)
	if err != nil || join.Room.ID != meeting.ID {
		t.Fatalf("join=%+v err=%v", join, err)
	}
	if first, err := owner.JoinRoom(ctx, ws.InviteCode, ""); err != nil || first.Room.ID != rooms[0].ID {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	if _, err := owner.JoinRoom(ctx, ws.InviteCode, "01ARZ3NDEKTSV4RRFFQ69G5FAV"); !apiclient.IsCode(err, "not_found") {
		t.Fatalf("err = %v", err)
	}

	guest := apiclient.New(base)
	if _, err := guest.CreateGuest(ctx, "guest", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := guest.CreateRoom(ctx, ws.InviteCode, "x"); !apiclient.IsCode(err, "forbidden") {
		t.Fatalf("err = %v", err)
	}
	if _, err := guest.Join(ctx, ws.InviteCode); err != nil {
		t.Fatal(err)
	}
	mine, err := guest.MyWorkspaces(ctx)
	if err != nil || len(mine) != 1 || mine[0].Role != "member" || mine[0].InviteCode != ws.InviteCode || mine[0].LastJoinedAt.IsZero() {
		t.Fatalf("mine=%+v err=%v", mine, err)
	}
	if mine, err := owner.MyWorkspaces(ctx); err != nil || len(mine) != 1 || mine[0].Role != "owner" {
		t.Fatalf("owner mine=%+v err=%v", mine, err)
	}
}
