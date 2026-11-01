package client

import (
	"image"
	"testing"
	"time"

	"github.com/o-ga09/gopher-office/internal/apiclient"
)

func TestSortWorkspaces(t *testing.T) {
	now := time.Now()
	mk := func(name string, ago time.Duration) apiclient.MyWorkspace {
		return apiclient.MyWorkspace{Workspace: apiclient.Workspace{Name: name}, LastJoinedAt: now.Add(-ago)}
	}
	in := []apiclient.MyWorkspace{mk("b", time.Hour), mk("c", time.Minute), mk("a", time.Hour)}
	got := sortWorkspaces(in)
	want := []string{"c", "a", "b"}
	for i, w := range want {
		if got[i].Name != w {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	if in[0].Name != "b" {
		t.Error("input was mutated")
	}
}

func TestIsOwner(t *testing.T) {
	ws := []apiclient.MyWorkspace{
		{Workspace: apiclient.Workspace{InviteCode: "x"}, Role: "owner"},
		{Workspace: apiclient.Workspace{InviteCode: "y"}, Role: "member"},
	}
	for code, want := range map[string]bool{"x": true, "y": false, "z": false} {
		if got := isOwner(ws, code); got != want {
			t.Errorf("isOwner(%q) = %v, want %v", code, got, want)
		}
	}
}

func TestSwitchTarget(t *testing.T) {
	tests := []struct {
		cur, target string
		want        string
		ok          bool
	}{
		{"a", "b", "b", true},
		{"a", "a", "", false},
		{"a", "", "", false},
		{"", "b", "b", true},
	}
	for _, tt := range tests {
		got, ok := switchTarget(tt.cur, tt.target)
		if got != tt.want || ok != tt.ok {
			t.Errorf("switchTarget(%q,%q) = %q,%v", tt.cur, tt.target, got, ok)
		}
	}
}

func TestRoomRowsAndNext(t *testing.T) {
	rooms := []apiclient.Room{{ID: "1", Name: "A"}, {ID: "2", Name: "B"}}
	rows := roomRows(rooms, "2")
	if rows[0].Current || !rows[1].Current {
		t.Errorf("rows = %+v", rows)
	}
	if got := nextRoomID(rooms, "1"); got != "2" {
		t.Errorf("next = %q", got)
	}
	if got := nextRoomID(rooms, "2"); got != "1" {
		t.Errorf("wrap = %q", got)
	}
	if got := nextRoomID(rooms, ""); got != "1" {
		t.Errorf("unselected = %q", got)
	}
	if got := nextRoomID(nil, "1"); got != "" {
		t.Errorf("empty = %q", got)
	}
}

func TestRowAt(t *testing.T) {
	area := image.Rect(10, 100, 210, 200)
	tests := []struct {
		p    image.Point
		want int
	}{{image.Pt(20, 100), 0}, {image.Pt(20, 139), 0}, {image.Pt(20, 140), 1}, {image.Pt(20, 160), 1}, {image.Pt(20, 190), 2}, {image.Pt(0, 110), -1}}
	for _, tt := range tests {
		if got := rowAt(tt.p, area, 40, 4); got != tt.want {
			t.Errorf("rowAt(%v) = %d, want %d", tt.p, got, tt.want)
		}
	}
	if got := rowAt(image.Pt(20, 190), area, 40, 2); got != -1 {
		t.Errorf("beyond n: %d", got)
	}
}

func TestWorkspacePathAndName(t *testing.T) {
	if got := workspacePath("a b"); got != "/w/a%20b" {
		t.Errorf("path = %q", got)
	}
	if got := nextRoomName(2); got != "Room 3" {
		t.Errorf("name = %q", got)
	}
}
