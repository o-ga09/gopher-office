package api

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/iotest"
)

func roomsOf(t *testing.T, env *testEnv, code string) []any {
	t.Helper()
	res := env.do(t, http.MethodGet, "/v1/workspaces/by-code/"+code, "", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("by-code: %d %s", res.Code, res.Body.String())
	}
	return res.json(t)["workspace"].(map[string]any)["rooms"].([]any)
}

func TestByCodeListsRooms(t *testing.T) {
	env := newTestEnv(t)
	_, tok := env.guest(t, "owner", 0)
	ws := env.workspace(t, tok, "Office")
	code := ws["invite_code"].(string)
	if rooms := roomsOf(t, env, code); len(rooms) != 1 || rooms[0].(map[string]any)["name"] != "Office" {
		t.Fatalf("rooms = %v", rooms)
	}
	if res := env.do(t, http.MethodPost, "/v1/workspaces/"+code+"/rooms", tok, map[string]any{"name": "Meeting"}); res.Code != http.StatusCreated {
		t.Fatalf("create room: %d %s", res.Code, res.Body.String())
	}
	rooms := roomsOf(t, env, code)
	if len(rooms) != 2 || rooms[1].(map[string]any)["name"] != "Meeting" || rooms[1].(map[string]any)["id"] == "" {
		t.Fatalf("rooms = %v", rooms)
	}
}

func TestJoinWithRoomID(t *testing.T) {
	env := newTestEnv(t)
	_, owner := env.guest(t, "owner", 0)
	_, guest := env.guest(t, "guest", 1)
	ws := env.workspace(t, owner, "Office")
	code := ws["invite_code"].(string)
	env.do(t, http.MethodPost, "/v1/workspaces/"+code+"/rooms", owner, map[string]any{"name": "Meeting"})
	rooms := roomsOf(t, env, code)
	first, second := rooms[0].(map[string]any)["id"].(string), rooms[1].(map[string]any)["id"].(string)
	path := "/v1/workspaces/" + code + "/join"

	roomOf := func(res response) string {
		t.Helper()
		if res.Code != http.StatusOK {
			t.Fatalf("join: %d %s", res.Code, res.Body.String())
		}
		m := res.json(t)
		claims, err := env.signer.ParseRoomToken(m["room_token"].(string))
		if err != nil {
			t.Fatal(err)
		}
		id := m["room"].(map[string]any)["id"].(string)
		if claims.RoomID != id {
			t.Fatalf("token room %q != response room %q", claims.RoomID, id)
		}
		return id
	}
	if got := roomOf(env.do(t, http.MethodPost, path, guest, map[string]any{"room_id": second})); got != second {
		t.Fatalf("room = %s, want %s", got, second)
	}
	if got := roomOf(env.do(t, http.MethodPost, path, guest, nil)); got != first {
		t.Fatalf("no body: room = %s, want first %s", got, first)
	}
	if got := roomOf(env.do(t, http.MethodPost, path, guest, "")); got != first {
		t.Fatalf("empty body: room = %s, want first", got)
	}
	if got := roomOf(env.do(t, http.MethodPost, path, guest, map[string]any{})); got != first {
		t.Fatalf("{}: room = %s, want first", got)
	}

	// 他のワークスペースのルームは 404
	_, other := env.guest(t, "other", 0)
	ows := env.workspace(t, other, "Other")
	otherRoom := roomsOf(t, env, ows["invite_code"].(string))[0].(map[string]any)["id"].(string)
	res := env.do(t, http.MethodPost, path, guest, map[string]any{"room_id": otherRoom})
	if res.Code != http.StatusNotFound {
		t.Fatalf("foreign room: %d", res.Code)
	}
	if res := env.do(t, http.MethodPost, path, guest, "not json"); res.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", res.Code)
	}
}

func TestCreateRoomPermissionsAndLimit(t *testing.T) {
	env := newTestEnv(t, func(c *Config) { c.MaxRoomsPerWorkspace = 2 })
	_, owner := env.guest(t, "owner", 0)
	_, guest := env.guest(t, "guest", 1)
	code := env.workspace(t, owner, "Office")["invite_code"].(string)
	path := "/v1/workspaces/" + code + "/rooms"

	if res := env.do(t, http.MethodPost, path, "", map[string]any{"name": "x"}); res.Code != http.StatusUnauthorized {
		t.Fatalf("anon: %d", res.Code)
	}
	res := env.do(t, http.MethodPost, path, guest, map[string]any{"name": "x"})
	if res.Code != http.StatusForbidden || res.errorCode(t) != "forbidden" {
		t.Fatalf("non-owner: %d %s", res.Code, res.Body.String())
	}
	if res := env.do(t, http.MethodPost, path, owner, map[string]any{"name": " "}); res.Code != http.StatusBadRequest {
		t.Fatalf("blank name: %d", res.Code)
	}
	if res := env.do(t, http.MethodPost, path, owner, map[string]any{"name": "B"}); res.Code != http.StatusCreated {
		t.Fatalf("second room: %d %s", res.Code, res.Body.String())
	}
	res = env.do(t, http.MethodPost, path, owner, map[string]any{"name": "C"})
	if res.Code != http.StatusConflict || res.errorCode(t) != "room_limit" {
		t.Fatalf("over limit: %d %s", res.Code, res.Body.String())
	}
	if res := env.do(t, http.MethodPost, "/v1/workspaces/zzzzzzzzzzzz/rooms", owner, map[string]any{"name": "x"}); res.Code != http.StatusNotFound {
		t.Fatalf("unknown code: %d", res.Code)
	}
}

func TestMyWorkspaces(t *testing.T) {
	env := newTestEnv(t)
	_, owner := env.guest(t, "owner", 0)
	_, guest := env.guest(t, "guest", 1)
	if res := env.do(t, http.MethodGet, "/v1/me/workspaces", "", nil); res.Code != http.StatusUnauthorized {
		t.Fatalf("anon: %d", res.Code)
	}
	list := func(tok string) []any {
		t.Helper()
		res := env.do(t, http.MethodGet, "/v1/me/workspaces", tok, nil)
		if res.Code != http.StatusOK {
			t.Fatalf("list: %d %s", res.Code, res.Body.String())
		}
		return res.json(t)["workspaces"].([]any)
	}
	if got := list(guest); len(got) != 0 {
		t.Fatalf("guest = %v", got)
	}

	ws := env.workspace(t, owner, "Office")
	code := ws["invite_code"].(string)
	got := list(owner)
	if len(got) != 1 || got[0].(map[string]any)["role"] != "owner" || got[0].(map[string]any)["invite_code"] != code {
		t.Fatalf("owner = %v", got)
	}

	if res := env.do(t, http.MethodPost, "/v1/workspaces/"+code+"/join", guest, nil); res.Code != http.StatusOK {
		t.Fatalf("join: %d", res.Code)
	}
	got = list(guest)
	if len(got) != 1 || got[0].(map[string]any)["role"] != "member" || got[0].(map[string]any)["name"] != "Office" || got[0].(map[string]any)["last_joined_at"] == "" {
		t.Fatalf("guest = %v", got)
	}
}

func TestMyWorkspacesOrderAndFailureTolerance(t *testing.T) {
	env := newTestEnv(t)
	_, owner := env.guest(t, "owner", 0)
	_, guest := env.guest(t, "guest", 1)
	a := env.workspace(t, owner, "A")["invite_code"].(string)
	b := env.workspace(t, owner, "B")["invite_code"].(string)
	env.do(t, http.MethodPost, "/v1/workspaces/"+a+"/join", guest, nil)
	env.do(t, http.MethodPost, "/v1/workspaces/"+b+"/join", guest, nil)
	env.do(t, http.MethodPost, "/v1/workspaces/"+a+"/join", guest, nil) // A に入り直す -> 先頭
	got := env.do(t, http.MethodGet, "/v1/me/workspaces", guest, nil).json(t)["workspaces"].([]any)
	if len(got) != 2 || got[0].(map[string]any)["name"] != "A" {
		t.Fatalf("order = %v", got)
	}

	// 記録に失敗しても入室はできる
	env.store.failMember = errors.New("db down")
	if res := env.do(t, http.MethodPost, "/v1/workspaces/"+b+"/join", guest, nil); res.Code != http.StatusOK {
		t.Fatalf("join with member failure: %d", res.Code)
	}
}

func TestJoinBodyReadErrors(t *testing.T) {
	env := newTestEnv(t)
	_, owner := env.guest(t, "owner", 0)
	code := env.workspace(t, owner, "Office")["invite_code"].(string)
	path := "/v1/workspaces/" + code + "/join"

	big := env.doRaw(http.MethodPost, path, owner, bytes.Repeat([]byte(" "), maxBodyBytes+1))
	if big.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: %d %s", big.Code, big.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, path, iotest.ErrReader(errors.New("boom")))
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("Authorization", "Bearer "+owner)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unreadable body: %d %s", rec.Code, rec.Body.String())
	}
}
