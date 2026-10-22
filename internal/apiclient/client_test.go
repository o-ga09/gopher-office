package apiclient_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/o-ga09/gopher-office/internal/api"
	"github.com/o-ga09/gopher-office/internal/apiclient"
	"github.com/o-ga09/gopher-office/internal/auth"
	"github.com/o-ga09/gopher-office/internal/migrate"
	"github.com/o-ga09/gopher-office/internal/testutil"
)

const (
	playerSecret = "client-test-player-secret-0123456789"
	roomSecret   = "client-test-room-secret-0123456789-x"
)

// 実際の API サーバ（と MySQL）に対して、クライアントを通す。
func startAPI(t *testing.T) *apiclient.Client {
	t.Helper()
	conn := testutil.OpenMySQL(t)
	if _, err := migrate.Up(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	db, err := api.NewGormDB(conn)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := auth.NewSigner(playerSecret, roomSecret)
	if err != nil {
		t.Fatal(err)
	}
	e, err := api.NewServer(api.Config{
		Store:     api.NewGormStore(db),
		Signer:    signer,
		Allocator: api.StaticAllocator{URL: "ws://gs.example/room"},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(e)
	t.Cleanup(ts.Close)
	return apiclient.New(ts.URL + "/") // 末尾の / は取り除かれる
}

func TestClientAgainstTheRealServer(t *testing.T) {
	ctx := context.Background()
	owner := startAPI(t)

	p, err := owner.CreateGuest(ctx, "owner", 0)
	if err != nil || p.DisplayName != "owner" || owner.Token() == "" {
		t.Fatalf("player=%+v token=%q err=%v", p, owner.Token(), err)
	}
	me, err := owner.Me(ctx)
	if err != nil || me.ID != p.ID {
		t.Fatalf("me=%+v err=%v", me, err)
	}
	if upd, err := owner.UpdateMe(ctx, "renamed", 2); err != nil || upd.DisplayName != "renamed" || upd.Gender != 2 {
		t.Fatalf("update=%+v err=%v", upd, err)
	}

	ws, err := owner.CreateWorkspace(ctx, "Office")
	if err != nil || len(ws.InviteCode) != api.InviteCodeLen || ws.InvitePath != "/w/"+ws.InviteCode {
		t.Fatalf("workspace=%+v err=%v", ws, err)
	}
	if name, err := owner.WorkspaceName(ctx, ws.InviteCode); err != nil || name != "Office" {
		t.Fatalf("name=%q err=%v", name, err)
	}

	join, err := owner.Join(ctx, ws.InviteCode)
	if err != nil {
		t.Fatal(err)
	}
	if join.ServerURL != "ws://gs.example/room" || join.RoomToken == "" || join.Room.ID == "" || join.Room.MapID != "office-1" || join.Workspace.Name != "Office" || join.ExpiresAt.IsZero() {
		t.Fatalf("join = %+v", join)
	}
}

func TestClientSurfacesAPIErrors(t *testing.T) {
	ctx := context.Background()
	c := startAPI(t)

	// 認証なし
	_, err := c.Me(ctx)
	if !apiclient.IsCode(err, "unauthorized") {
		t.Fatalf("err = %v, want unauthorized", err)
	}
	if ae2, ok := err.(*apiclient.Error); !ok || ae2.Status != http.StatusUnauthorized { //nolint:errorlint // 型を直接確認する
		t.Fatalf("err = %#v", err)
	}

	// 存在しない招待コード
	if _, err := c.WorkspaceName(ctx, "zzzzzzzzzzzz"); !apiclient.IsCode(err, "not_found") {
		t.Fatalf("err = %v, want not_found", err)
	}
	// 不正な入力
	if _, err := c.CreateGuest(ctx, "x", 9); !apiclient.IsCode(err, "invalid_request") {
		t.Fatalf("err = %v, want invalid_request", err)
	}
	// 招待コードにパス区切りが入っていても、別のエンドポイントを叩かない（エスケープされる）
	if _, err := c.Join(ctx, "../../me"); err == nil {
		t.Fatal("a path-like code must fail")
	}
}

func TestClientHandlesNonJSONErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "<html>bad gateway</html>", http.StatusBadGateway) // プロキシが返す HTML など
	}))
	defer ts.Close()

	_, err := apiclient.New(ts.URL).Me(context.Background())
	ae, ok := err.(*apiclient.Error) //nolint:errorlint // 型を直接確認する
	if !ok || ae.Status != http.StatusBadGateway {
		t.Fatalf("err = %#v", err)
	}
}
