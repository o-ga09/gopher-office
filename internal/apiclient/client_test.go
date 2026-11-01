package apiclient_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
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
	return apiclient.New(startServer(t) + "/") // 末尾の / は取り除かれる
}

// startServer は API サーバを起動して、そのベース URL を返す。
func startServer(t *testing.T) string {
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
	return ts.URL
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

func TestClientIcon(t *testing.T) {
	ctx := context.Background()
	c := startAPI(t)
	p, err := c.CreateGuest(ctx, "iconic", 1)
	if err != nil || p.IconHash != nil {
		t.Fatalf("player=%+v err=%v", p, err)
	}

	var src bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 200, 100))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	if err := png.Encode(&src, img); err != nil {
		t.Fatal(err)
	}
	up, err := c.UploadIcon(ctx, src.Bytes())
	if err != nil || up.IconHash == nil {
		t.Fatalf("upload: %+v err=%v", up, err)
	}

	data, err := c.Icon(ctx, p.ID, *up.IconHash)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := png.Decode(bytes.NewReader(data)); err != nil || got.Bounds().Dx() != 64 {
		t.Fatalf("icon: %v %v", got, err)
	}

	if _, err := c.UploadIcon(ctx, []byte("not an image")); !apiclient.IsCode(err, "unsupported_image") {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.Icon(ctx, "01ARZ3NDEKTSV4RRFFQ69G5FAV", *up.IconHash); !apiclient.IsCode(err, "not_found") {
		t.Fatalf("err = %v", err)
	}
}

func TestJoinResultDialURL(t *testing.T) {
	for _, c := range []struct{ url, token, want string }{
		{"wss://office.example/room", "a.b.c", "wss://office.example/room?token=a.b.c"},
		{"ws://localhost:7654/room?x=1", "a b", "ws://localhost:7654/room?token=a+b&x=1"},
		{"wss://office.example/room", "x&y=z", "wss://office.example/room?token=x%26y%3Dz"},   // トークンに & や = があっても、別のクエリにならない
		{"wss://office.example/room?token=old", "new", "wss://office.example/room?token=new"}, // 既存の token は置き換える
	} {
		j := apiclient.JoinResult{ServerURL: c.url, RoomToken: c.token}
		if got := j.DialURL(); got != c.want {
			t.Errorf("DialURL(%q, %q) = %q, want %q", c.url, c.token, got, c.want)
		}
	}
}
