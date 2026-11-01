package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/o-ga09/gopher-office/internal/auth"
	"github.com/o-ga09/gopher-office/internal/migrate"
	"github.com/o-ga09/gopher-office/internal/testutil"
)

// newGormStore は、マイグレーション済みの専用データベースに繋いだ Store を返す（TEST_MYSQL_DSN が必要）。
func newGormStore(t *testing.T) *GormStore {
	t.Helper()
	conn := testutil.OpenMySQL(t)
	if _, err := migrate.Up(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	db, err := NewGormDB(conn)
	if err != nil {
		t.Fatal(err)
	}
	return NewGormStore(db)
}

func newPlayer(name string) *Player {
	return &Player{ID: ulid.Make().String(), DisplayName: name, Gender: 1, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
}

func TestGormStorePlayers(t *testing.T) {
	s := newGormStore(t)
	ctx := context.Background()

	p := newPlayer("🐹 たいち")
	if err := s.CreatePlayer(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetPlayer(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "🐹 たいち" || got.Gender != 1 || got.IconHash != nil || !got.CreatedAt.Equal(p.CreatedAt) {
		t.Fatalf("round trip mismatch: %+v vs %+v", got, p)
	}

	if _, err := s.GetPlayer(ctx, ulid.Make().String()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing player: err = %v", err)
	}

	name, gender := "renamed", uint8(2)
	upd, err := s.UpdatePlayer(ctx, p.ID, PlayerUpdate{DisplayName: &name})
	if err != nil || upd.DisplayName != "renamed" || upd.Gender != 1 {
		t.Fatalf("a partial update must keep the other fields: %+v err=%v", upd, err)
	}
	upd, err = s.UpdatePlayer(ctx, p.ID, PlayerUpdate{Gender: &gender})
	if err != nil || upd.Gender != 2 || upd.DisplayName != "renamed" {
		t.Fatalf("got %+v err=%v", upd, err)
	}
	if again, _ := s.GetPlayer(ctx, p.ID); again.DisplayName != "renamed" || again.Gender != 2 {
		t.Fatalf("the update must be persisted: %+v", again)
	}
	if _, err := s.UpdatePlayer(ctx, ulid.Make().String(), PlayerUpdate{DisplayName: &name}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("updating a missing player: err = %v", err)
	}
}

func newWorkspace(owner, code string) (*Workspace, *Room) {
	w := &Workspace{ID: ulid.Make().String(), OwnerID: owner, Name: "ws-" + code, InviteCode: code, MaxPlayers: 200, CreatedAt: time.Now().UTC()}
	return w, &Room{ID: ulid.Make().String(), WorkspaceID: w.ID, Name: w.Name, MapID: "office-1"}
}

func TestGormStoreWorkspaces(t *testing.T) {
	s := newGormStore(t)
	ctx := context.Background()
	owner := newPlayer("owner")
	if err := s.CreatePlayer(ctx, owner); err != nil {
		t.Fatal(err)
	}

	w, r := newWorkspace(owner.ID, "aBcDeFgHiJkL")
	if err := s.CreateWorkspace(ctx, w, r); err != nil {
		t.Fatal(err)
	}
	gw, gr, err := s.GetWorkspaceByCode(ctx, "aBcDeFgHiJkL")
	if err != nil || gw.ID != w.ID || gr.ID != r.ID || gr.WorkspaceID != w.ID || gr.MapID != "office-1" || gw.MaxPlayers != 200 {
		t.Fatalf("workspace=%+v room=%+v err=%v", gw, gr, err)
	}
	if n, err := s.CountWorkspacesByOwner(ctx, owner.ID); err != nil || n != 1 {
		t.Fatalf("count = %d err=%v", n, err)
	}

	// 招待コードは大文字小文字を区別する（別のコードとして共存でき、照合も厳密）
	w2, r2 := newWorkspace(owner.ID, "abcdefghijkl")
	if err := s.CreateWorkspace(ctx, w2, r2); err != nil {
		t.Fatalf("codes differing only in case must coexist: %v", err)
	}
	if got, _, _ := s.GetWorkspaceByCode(ctx, "abcdefghijkl"); got == nil || got.ID != w2.ID {
		t.Fatal("lookup must be exact (case-sensitive)")
	}
	if _, _, err := s.GetWorkspaceByCode(ctx, "ABCDEFGHIJKL"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a differently-cased code must not match: err = %v", err)
	}
	if _, _, err := s.GetWorkspaceByCode(ctx, "zzzzzzzzzzzz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown code: err = %v", err)
	}
}

func TestGormStoreCreateWorkspaceIsAtomic(t *testing.T) {
	s := newGormStore(t)
	ctx := context.Background()
	owner := newPlayer("owner")
	if err := s.CreatePlayer(ctx, owner); err != nil {
		t.Fatal(err)
	}
	first, firstRoom := newWorkspace(owner.ID, "dupdupdupdup")
	if err := s.CreateWorkspace(ctx, first, firstRoom); err != nil {
		t.Fatal(err)
	}

	// 招待コードの重複: ErrDuplicate になり、ルームだけが残ることもない
	dup, dupRoom := newWorkspace(owner.ID, "dupdupdupdup")
	if err := s.CreateWorkspace(ctx, dup, dupRoom); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
	var rooms int64
	if err := s.db.Model(&Room{}).Where("id = ?", dupRoom.ID).Count(&rooms).Error; err != nil || rooms != 0 {
		t.Fatalf("a failed creation must not leave a room behind (rooms=%d err=%v)", rooms, err)
	}

	// 重複以外のエラー（存在しない所有者）は ErrDuplicate として扱わない
	orphan, orphanRoom := newWorkspace(ulid.Make().String(), "orphanorphan")
	err := s.CreateWorkspace(ctx, orphan, orphanRoom)
	if err == nil || errors.Is(err, ErrDuplicate) {
		t.Fatalf("a missing owner must fail with a different error, got %v", err)
	}

	// ルームの作成に失敗したら、ワークスペースも作られない
	w, r := newWorkspace(owner.ID, "rollbackrbck")
	r.ID = firstRoom.ID // 主キーの衝突を起こす
	if err := s.CreateWorkspace(ctx, w, r); err == nil {
		t.Fatal("a primary key collision must fail")
	}
	if _, _, err := s.GetWorkspaceByCode(ctx, "rollbackrbck"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the workspace must be rolled back with its room: err = %v", err)
	}
}

func TestGormStorePing(t *testing.T) {
	s := newGormStore(t)
	if err := s.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// 実際の MySQL を使って、ゲスト作成から入室まで（API 全体）を通す。
func TestEndToEndWithMySQL(t *testing.T) {
	store := newGormStore(t)
	env := newTestEnv(t, func(c *Config) { c.Store = store })

	_, ownerToken := env.guest(t, "オーナー", 0)
	ws := env.workspace(t, ownerToken, "LT デモ 🐹")
	code := ws["invite_code"].(string)

	// 招待ページ用の公開エンドポイント
	res := env.do(t, http.MethodGet, "/v1/workspaces/by-code/"+code, "", nil)
	if res.Code != http.StatusOK || res.json(t)["workspace"].(map[string]any)["name"] != "LT デモ 🐹" {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}

	// 招待された人が入室する。roomToken は GameServer（検証鍵だけ）で検証できる
	visitorID, visitorToken := env.guest(t, "visitor", 1)
	res = env.do(t, http.MethodPost, "/v1/workspaces/"+code+"/join", visitorToken, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	m := res.json(t)
	v, _ := auth.NewRoomVerifier(testRoomSecret, func() string { return m["room"].(map[string]any)["id"].(string) })
	if id, err := v.Verify(m["room_token"].(string)); err != nil || id != visitorID {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

// 同時に作っても、招待コードは重複しない（一意制約と再試行が機能する）。
func TestConcurrentWorkspaceCreationYieldsUniqueCodes(t *testing.T) {
	store := newGormStore(t)
	env := newTestEnv(t, func(c *Config) {
		c.Store = store
		c.MaxWorkspacesPerPlayer = 100
		c.ActionPerMinute = 1000
	})

	const n = 20
	tokens := make([]string, n)
	for i := range tokens {
		_, tokens[i] = env.guest(t, fmt.Sprintf("owner-%d", i), 0)
	}

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		codes = map[string]bool{}
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := env.do(t, http.MethodPost, "/v1/workspaces", tokens[i], map[string]any{"name": "w"})
			if res.Code != http.StatusCreated {
				t.Errorf("owner %d: status=%d body=%s", i, res.Code, res.Body.String())
				return
			}
			mu.Lock()
			codes[res.json(t)["workspace"].(map[string]any)["invite_code"].(string)] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(codes) != n {
		t.Fatalf("got %d distinct codes for %d workspaces", len(codes), n)
	}
}
