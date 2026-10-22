package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/o-ga09/gopher-office/internal/auth"
)

const (
	testPlayerSecret = "test-player-secret-0123456789-0123456789"
	testRoomSecret   = "test-room-secret-0123456789-0123456789-x"
)

// memStore はハンドラのテスト用の、メモリ上の Store。
type memStore struct {
	mu         sync.Mutex
	players    map[string]*Player
	workspaces map[string]*Workspace
	rooms      map[string]*Room

	forceDuplicates int   // CreateWorkspace が ErrDuplicate を返す回数
	failCreate      error // CreatePlayer が返すエラー
	pingErr         error
}

func newMemStore() *memStore {
	return &memStore{players: map[string]*Player{}, workspaces: map[string]*Workspace{}, rooms: map[string]*Room{}}
}

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
		for _, r := range s.rooms {
			if r.WorkspaceID == w.ID {
				cw, cr := *w, *r
				return &cw, &cr, nil
			}
		}
	}
	return nil, nil, ErrNotFound
}

func (s *memStore) Ping(context.Context) error { return s.pingErr }

type failingAllocator struct{}

func (failingAllocator) Allocate(context.Context, *Room) (Allocation, error) {
	return Allocation{}, errors.New("agones exploded: secret-internal-detail")
}

type testEnv struct {
	e      *echo.Echo
	store  *memStore
	signer *auth.Signer
}

func newTestEnv(t *testing.T, mutate ...func(*Config)) *testEnv {
	t.Helper()
	signer, err := auth.NewSigner(testPlayerSecret, testRoomSecret)
	if err != nil {
		t.Fatal(err)
	}
	store := newMemStore()
	cfg := Config{
		Store:     store,
		Signer:    signer,
		Allocator: StaticAllocator{URL: "ws://gs.example/room"},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, m := range mutate {
		m(&cfg)
	}
	e, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{e: e, store: store, signer: signer}
}

type response struct {
	*httptest.ResponseRecorder
}

func (r response) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &m); err != nil {
		t.Fatalf("response is not JSON: %v\nbody: %s", err, r.Body.String())
	}
	return m
}

func (r response) errorCode(t *testing.T) string {
	t.Helper()
	e, _ := r.json(t)["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func (env *testEnv) do(t *testing.T, method, path, token string, body any) response {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.1:1234"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	return response{rec}
}

// guest はゲストを作り、そのプレイヤー ID とトークンを返す。
func (env *testEnv) guest(t *testing.T, name string, gender int) (id, token string) {
	t.Helper()
	res := env.do(t, http.MethodPost, "/v1/guest", "", map[string]any{"display_name": name, "gender": gender})
	if res.Code != http.StatusCreated {
		t.Fatalf("guest: status %d: %s", res.Code, res.Body.String())
	}
	m := res.json(t)
	return m["player"].(map[string]any)["id"].(string), m["player_token"].(string)
}

func (env *testEnv) workspace(t *testing.T, token, name string) map[string]any {
	t.Helper()
	res := env.do(t, http.MethodPost, "/v1/workspaces", token, map[string]any{"name": name})
	if res.Code != http.StatusCreated {
		t.Fatalf("create workspace: status %d: %s", res.Code, res.Body.String())
	}
	return res.json(t)["workspace"].(map[string]any)
}

func TestCreateGuest(t *testing.T) {
	env := newTestEnv(t)
	res := env.do(t, http.MethodPost, "/v1/guest", "", map[string]any{"display_name": "たいち🐹", "gender": 1})
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	m := res.json(t)
	p := m["player"].(map[string]any)
	if p["display_name"] != "たいち🐹" || p["gender"] != float64(1) || p["id"] == "" {
		t.Fatalf("unexpected player: %v", p)
	}
	id, err := env.signer.ParsePlayerToken(m["player_token"].(string))
	if err != nil || id != p["id"] {
		t.Fatalf("the token must be for the new player: id=%q err=%v", id, err)
	}
}

func TestGuestNameIsSanitized(t *testing.T) {
	env := newTestEnv(t)
	_, token := env.guest(t, "  a\nb\u202ec"+strings.Repeat("あ", 50), 0)
	p := env.do(t, http.MethodGet, "/v1/me", token, nil).json(t)["player"].(map[string]any)
	name := p["display_name"].(string)
	if strings.ContainsAny(name, "\n\u202e") {
		t.Fatalf("control and format characters must be removed: %q", name)
	}
	if n := len([]rune(name)); n != 32 {
		t.Fatalf("name must be cut to 32 characters, got %d", n)
	}
	// 空の名前は既定名になる
	_, token = env.guest(t, "", 2)
	p = env.do(t, http.MethodGet, "/v1/me", token, nil).json(t)["player"].(map[string]any)
	if p["display_name"] != "Gopher" {
		t.Fatalf("empty name must fall back, got %v", p["display_name"])
	}
}

func TestGuestValidation(t *testing.T) {
	env := newTestEnv(t)
	cases := map[string]any{
		"missing gender":    map[string]any{"display_name": "x"},
		"gender too large":  map[string]any{"display_name": "x", "gender": 3},
		"gender negative":   map[string]any{"display_name": "x", "gender": -1},
		"name too long raw": map[string]any{"display_name": strings.Repeat("a", 257), "gender": 0},
		"not json":          "this is not json",
		"wrong type":        map[string]any{"display_name": 123, "gender": 0},
	}
	for name, body := range cases {
		res := env.do(t, http.MethodPost, "/v1/guest", "", body)
		if res.Code != http.StatusBadRequest || res.errorCode(t) != "invalid_request" {
			t.Errorf("%s: status=%d body=%s", name, res.Code, res.Body.String())
		}
	}

	// どのフィールドが悪いかを返す（JSON の名前で）
	res := env.do(t, http.MethodPost, "/v1/guest", "", map[string]any{"display_name": "x"})
	fields := res.json(t)["error"].(map[string]any)["fields"].(map[string]any)
	if _, ok := fields["gender"]; !ok {
		t.Fatalf("the failing field must be reported by its JSON name, got %v", fields)
	}
}

func TestBodyTooLarge(t *testing.T) {
	env := newTestEnv(t)
	huge := `{"display_name":"` + strings.Repeat("a", maxBodyBytes) + `","gender":0}`
	res := env.do(t, http.MethodPost, "/v1/guest", "", huge)
	if res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", res.Code)
	}
}

func TestErrorsAreJSON(t *testing.T) {
	env := newTestEnv(t)
	if res := env.do(t, http.MethodGet, "/v1/nope", "", nil); res.Code != http.StatusNotFound || res.errorCode(t) != "not_found" {
		t.Fatalf("404: status=%d body=%s", res.Code, res.Body.String())
	}
	if res := env.do(t, http.MethodDelete, "/v1/guest", "", nil); res.Code != http.StatusMethodNotAllowed || res.errorCode(t) != "method_not_allowed" {
		t.Fatalf("405: status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestAuthentication(t *testing.T) {
	env := newTestEnv(t)
	id, token := env.guest(t, "me", 0)

	roomTok, _, _ := env.signer.IssueRoomToken(auth.RoomClaims{PlayerID: id, RoomID: "r"})
	for name, tok := range map[string]string{"no token": "", "garbage": "garbage", "a room token": roomTok} {
		if res := env.do(t, http.MethodGet, "/v1/me", tok, nil); res.Code != http.StatusUnauthorized || res.errorCode(t) != "unauthorized" {
			t.Errorf("%s: status=%d", name, res.Code)
		}
	}
	// "Bearer" 以外の方式
	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Authorization", "Basic "+token)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("non-bearer scheme: status=%d", rec.Code)
	}

	if res := env.do(t, http.MethodGet, "/v1/me", token, nil); res.Code != http.StatusOK {
		t.Fatalf("valid token: status=%d", res.Code)
	}

	// プレイヤーが削除されたら、署名が有効でも使えない
	delete(env.store.players, id)
	if res := env.do(t, http.MethodGet, "/v1/me", token, nil); res.Code != http.StatusUnauthorized {
		t.Fatalf("deleted player: status=%d", res.Code)
	}
}

func TestUpdateMe(t *testing.T) {
	env := newTestEnv(t)
	_, token := env.guest(t, "before", 0)

	res := env.do(t, http.MethodPatch, "/v1/me", token, map[string]any{"display_name": "after\n"})
	p := res.json(t)["player"].(map[string]any)
	if res.Code != http.StatusOK || p["display_name"] != "after" || p["gender"] != float64(0) {
		t.Fatalf("status=%d player=%v", res.Code, p)
	}
	res = env.do(t, http.MethodPatch, "/v1/me", token, map[string]any{"gender": 2})
	if p = res.json(t)["player"].(map[string]any); p["gender"] != float64(2) || p["display_name"] != "after" {
		t.Fatalf("a partial update must keep the other field: %v", p)
	}

	for name, body := range map[string]any{
		"empty body":     map[string]any{},
		"invalid gender": map[string]any{"gender": 9},
	} {
		if res := env.do(t, http.MethodPatch, "/v1/me", token, body); res.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d", name, res.Code)
		}
	}
	if res := env.do(t, http.MethodPatch, "/v1/me", "", map[string]any{"gender": 1}); res.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated: status=%d", res.Code)
	}
}

func TestCreateWorkspace(t *testing.T) {
	env := newTestEnv(t)
	_, token := env.guest(t, "owner", 0)

	w := env.workspace(t, token, "  LT\nデモ  ")
	code := w["invite_code"].(string)
	if len(code) != InviteCodeLen || strings.Trim(code, base62) != "" {
		t.Fatalf("invite code %q must be %d base62 characters", code, InviteCodeLen)
	}
	if w["invite_path"] != "/w/"+code || w["name"] != "LTデモ" || w["max_players"] != float64(200) {
		t.Fatalf("unexpected workspace: %v", w)
	}

	if res := env.do(t, http.MethodPost, "/v1/workspaces", "", map[string]any{"name": "x"}); res.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated: status=%d", res.Code)
	}
	for name, body := range map[string]any{
		"missing name":        map[string]any{},
		"only invisible name": map[string]any{"name": "\u200b \n"},
	} {
		if res := env.do(t, http.MethodPost, "/v1/workspaces", token, body); res.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d", name, res.Code)
		}
	}
}

func TestWorkspaceLimitPerPlayer(t *testing.T) {
	env := newTestEnv(t, func(c *Config) { c.MaxWorkspacesPerPlayer = 2 })
	_, token := env.guest(t, "owner", 0)
	env.workspace(t, token, "one")
	env.workspace(t, token, "two")

	res := env.do(t, http.MethodPost, "/v1/workspaces", token, map[string]any{"name": "three"})
	if res.Code != http.StatusConflict || res.errorCode(t) != "workspace_limit" {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	// ほかのプレイヤーには影響しない
	_, other := env.guest(t, "other", 1)
	env.workspace(t, other, "mine")
}

func TestInviteCodeCollisionIsRetried(t *testing.T) {
	env := newTestEnv(t)
	_, token := env.guest(t, "owner", 0)

	env.store.forceDuplicates = 2
	env.workspace(t, token, "retried") // 2 回衝突しても、作り直して成功する

	env.store.forceDuplicates = 100
	res := env.do(t, http.MethodPost, "/v1/workspaces", token, map[string]any{"name": "doomed"})
	if res.Code != http.StatusInternalServerError || res.errorCode(t) != "internal" {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestGetWorkspaceByCodeIsPublicAndMinimal(t *testing.T) {
	env := newTestEnv(t)
	ownerID, token := env.guest(t, "owner", 0)
	code := env.workspace(t, token, "Public Name")["invite_code"].(string)

	res := env.do(t, http.MethodGet, "/v1/workspaces/by-code/"+code, "", nil) // 認証なし
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d", res.Code)
	}
	w := res.json(t)["workspace"].(map[string]any)
	if w["name"] != "Public Name" {
		t.Fatalf("workspace = %v", w)
	}
	if body := res.Body.String(); strings.Contains(body, ownerID) || strings.Contains(body, code) {
		t.Fatalf("the public endpoint must not expose the owner or the invite code: %s", body)
	}

	for name, path := range map[string]string{
		"unknown code":     "/v1/workspaces/by-code/aaaaaaaaaaaa",
		"too short":        "/v1/workspaces/by-code/abc",
		"too long":         "/v1/workspaces/by-code/" + strings.Repeat("a", 40),
		"non alphanumeric": "/v1/workspaces/by-code/abc-def-ghij",
		"sql injection":    "/v1/workspaces/by-code/%27%20OR%20%271%27%3D%271",
	} {
		if res := env.do(t, http.MethodGet, path, "", nil); res.Code != http.StatusNotFound {
			t.Errorf("%s: status=%d", name, res.Code)
		}
	}
}

func TestJoinWorkspace(t *testing.T) {
	env := newTestEnv(t)
	_, ownerToken := env.guest(t, "owner", 0)
	code := env.workspace(t, ownerToken, "Office")["invite_code"].(string)
	guestID, guestToken := env.guest(t, "visitor", 2)

	res := env.do(t, http.MethodPost, "/v1/workspaces/"+code+"/join", guestToken, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	m := res.json(t)
	if m["server_url"] != "ws://gs.example/room" {
		t.Fatalf("server_url = %v", m["server_url"])
	}
	room := m["room"].(map[string]any)

	// GameServer 側の検証（検証鍵だけを持つ）を通り、プレイヤーとルームが正しい
	v, err := auth.NewRoomVerifier(testRoomSecret, func() string { return room["id"].(string) })
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.Verify(m["room_token"].(string))
	if err != nil || got != guestID {
		t.Fatalf("the room token must verify for the guest: id=%q err=%v", got, err)
	}
	if exp, err := time.Parse(time.RFC3339, m["expires_at"].(string)); err != nil || time.Until(exp) > auth.RoomTokenTTL || time.Until(exp) < 30*time.Second {
		t.Fatalf("expires_at = %v (%v)", m["expires_at"], err)
	}
	// 別のルームの GameServer では使えない
	other, _ := auth.NewRoomVerifier(testRoomSecret, func() string { return "another-room" })
	if _, err := other.Verify(m["room_token"].(string)); err == nil {
		t.Fatal("the token must be bound to the room")
	}

	// 認証なし・存在しない招待コード
	if res := env.do(t, http.MethodPost, "/v1/workspaces/"+code+"/join", "", nil); res.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated: status=%d", res.Code)
	}
	if res := env.do(t, http.MethodPost, "/v1/workspaces/zzzzzzzzzzzz/join", guestToken, nil); res.Code != http.StatusNotFound {
		t.Errorf("unknown code: status=%d", res.Code)
	}
}

func TestJoinWhenNoRoomIsAvailable(t *testing.T) {
	env := newTestEnv(t, func(c *Config) { c.Allocator = failingAllocator{} })
	_, token := env.guest(t, "owner", 0)
	code := env.workspace(t, token, "Office")["invite_code"].(string)

	res := env.do(t, http.MethodPost, "/v1/workspaces/"+code+"/join", token, nil)
	if res.Code != http.StatusServiceUnavailable || res.errorCode(t) != "unavailable" {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "secret-internal-detail") {
		t.Fatal("internal error details must not reach the client")
	}
}

func TestInternalErrorsDoNotLeakDetails(t *testing.T) {
	env := newTestEnv(t)
	env.store.failCreate = errors.New("Error 1045: Access denied for user 'root'@'10.0.0.5' (password: YES)")
	res := env.do(t, http.MethodPost, "/v1/guest", "", map[string]any{"display_name": "x", "gender": 0})
	if res.Code != http.StatusInternalServerError || res.errorCode(t) != "internal" {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "Access denied") || strings.Contains(res.Body.String(), "10.0.0.5") {
		t.Fatalf("database errors must not reach the client: %s", res.Body.String())
	}
}

func TestGuestRateLimitPerIP(t *testing.T) {
	env := newTestEnv(t, func(c *Config) { c.GuestPerMinute = 3 })
	for i := 0; i < 3; i++ {
		if res := env.do(t, http.MethodPost, "/v1/guest", "", map[string]any{"gender": 0}); res.Code != http.StatusCreated {
			t.Fatalf("request %d: status=%d", i, res.Code)
		}
	}
	res := env.do(t, http.MethodPost, "/v1/guest", "", map[string]any{"gender": 0})
	if res.Code != http.StatusTooManyRequests || res.errorCode(t) != "rate_limited" {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}

	// 別の IP は影響を受けない
	req := httptest.NewRequest(http.MethodPost, "/v1/guest", strings.NewReader(`{"gender":0}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "198.51.100.9:4321"
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("another IP must not be limited: status=%d", rec.Code)
	}
}

func TestCloudflareIPIsOnlyTrustedWhenEnabled(t *testing.T) {
	send := func(env *testEnv, cfIP string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/guest", strings.NewReader(`{"gender":0}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("CF-Connecting-IP", cfIP)
		req.RemoteAddr = "192.0.2.1:1234"
		rec := httptest.NewRecorder()
		env.e.ServeHTTP(rec, req)
		return rec.Code
	}

	// 信頼しない（既定）: ヘッダを変えても、同じ接続元として数える（偽装して制限を逃れられない）
	untrusted := newTestEnv(t, func(c *Config) { c.GuestPerMinute = 1 })
	send(untrusted, "203.0.113.1")
	if code := send(untrusted, "203.0.113.2"); code != http.StatusTooManyRequests {
		t.Fatalf("a spoofed header must not bypass the limit when untrusted: status=%d", code)
	}

	// 信頼する: ヘッダの IP ごとに数える
	trusted := newTestEnv(t, func(c *Config) { c.GuestPerMinute = 1; c.TrustCloudflareIP = true })
	send(trusted, "203.0.113.1")
	if code := send(trusted, "203.0.113.2"); code != http.StatusCreated {
		t.Fatalf("different client IPs must be counted separately: status=%d", code)
	}
	if code := send(trusted, "203.0.113.1"); code != http.StatusTooManyRequests {
		t.Fatalf("the same client IP must be limited: status=%d", code)
	}
}

func TestHealthz(t *testing.T) {
	env := newTestEnv(t)
	if res := env.do(t, http.MethodGet, "/healthz", "", nil); res.Code != http.StatusOK {
		t.Fatalf("status=%d", res.Code)
	}
	env.store.pingErr = errors.New("db down")
	if res := env.do(t, http.MethodGet, "/healthz", "", nil); res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", res.Code)
	}
}

func TestNewServerRequiresDependencies(t *testing.T) {
	signer, _ := auth.NewSigner(testPlayerSecret, testRoomSecret)
	for name, cfg := range map[string]Config{
		"no store":     {Signer: signer, Allocator: StaticAllocator{}},
		"no signer":    {Store: newMemStore(), Allocator: StaticAllocator{}},
		"no allocator": {Store: newMemStore(), Signer: signer},
	} {
		if _, err := NewServer(cfg); err == nil {
			t.Errorf("%s: must fail", name)
		}
	}
}

func TestCORSIsOffByDefaultAndLimitedToTheAllowedOrigins(t *testing.T) {
	preflight := func(env *testEnv, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodOptions, "/v1/guest", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
		rec := httptest.NewRecorder()
		env.e.ServeHTTP(rec, req)
		return rec
	}

	off := newTestEnv(t)
	if got := preflight(off, "http://evil.example").Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("CORS must be disabled by default, got %q", got)
	}

	on := newTestEnv(t, func(c *Config) { c.CORSOrigins = []string{"http://localhost:7654"} })
	allowed := preflight(on, "http://localhost:7654")
	if got := allowed.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:7654" {
		t.Fatalf("allowed origin: Access-Control-Allow-Origin = %q", got)
	}
	if got := strings.ToLower(allowed.Header().Get("Access-Control-Allow-Headers")); !strings.Contains(got, "authorization") {
		t.Fatalf("Authorization must be allowed, got %q", got)
	}
	if got := preflight(on, "http://evil.example").Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("a disallowed origin must not be granted access, got %q", got)
	}
	if got := preflight(on, "*").Header().Get("Access-Control-Allow-Origin"); got == "*" {
		t.Fatal("a wildcard must never be granted")
	}
}
