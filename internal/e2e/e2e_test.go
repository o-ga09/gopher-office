// Package e2e は、API・GameServer・MySQL を実物でつないだ結合テスト（TEST_MYSQL_DSN が必要）。
package e2e

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	officev1 "github.com/o-ga09/gopher-office/gen/office/v1"
	"github.com/o-ga09/gopher-office/internal/api"
	"github.com/o-ga09/gopher-office/internal/apiclient"
	"github.com/o-ga09/gopher-office/internal/auth"
	"github.com/o-ga09/gopher-office/internal/gameserver"
	"github.com/o-ga09/gopher-office/internal/migrate"
	"github.com/o-ga09/gopher-office/internal/netcode"
	"github.com/o-ga09/gopher-office/internal/testutil"
)

const (
	signKeyPlayer = "e2e-player-secret-0123456789-0123456789"
	signKeyRoom   = "e2e-room-secret-0123456789-0123456789-x"
)

type stack struct {
	apiURL string
	gsURL  string
	// boundRoom は GameServer が担当するルーム ID。空の間は、どのルームのトークンも受け付ける
	boundRoom string
}

func startStack(t *testing.T) *stack {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := &stack{}

	// GameServer（roomToken の検証鍵だけを持つ）
	verifier, err := auth.NewRoomVerifier(signKeyRoom, func() string { return st.boundRoom })
	if err != nil {
		t.Fatal(err)
	}
	gs := gameserver.NewServer(gameserver.Config{MaxPlayers: 10, Verifier: verifier, Logger: logger})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { gs.Room().Run(ctx); close(done) }()
	gsTS := httptest.NewServer(gs.Handler())
	st.gsURL = "ws" + strings.TrimPrefix(gsTS.URL, "http") + "/room"

	// API（両方の鍵を持つ）
	conn := testutil.OpenMySQL(t)
	if _, err := migrate.Up(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	db, err := api.NewGormDB(conn)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := auth.NewSigner(signKeyPlayer, signKeyRoom)
	if err != nil {
		t.Fatal(err)
	}
	e, err := api.NewServer(api.Config{
		Store:     api.NewGormStore(db),
		Signer:    signer,
		Allocator: api.StaticAllocator{URL: st.gsURL},
		Logger:    logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	apiTS := httptest.NewServer(e)
	st.apiURL = apiTS.URL

	t.Cleanup(func() {
		apiTS.Close()
		gsTS.Close()
		cancel()
		<-done
	})
	return st
}

// firstMessage は GameServer に接続して Hello を送り、最初のメッセージを返す。
func firstMessage(t *testing.T, url, roomToken, name string) (*officev1.ServerMessage, *netcode.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := netcode.Dial(ctx, url, &officev1.Hello{
		RoomToken: roomToken,
		Profile:   &officev1.Profile{DisplayName: name, Gender: officev1.Gender_GENDER_FEMALE},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	select {
	case m, ok := <-c.Recv():
		if !ok {
			t.Fatalf("connection closed before any message: %v", c.Err())
		}
		return m, c
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the first message")
		return nil, nil
	}
}

func TestInviteToWelcome(t *testing.T) {
	st := startStack(t)
	ctx := context.Background()

	// オーナーがワークスペースを作り、招待コードを共有する
	owner := apiclient.New(st.apiURL)
	if _, err := owner.CreateGuest(ctx, "owner", 0); err != nil {
		t.Fatal(err)
	}
	ws, err := owner.CreateWorkspace(ctx, "LT デモ")
	if err != nil {
		t.Fatal(err)
	}

	// 招待された人が、招待コードだけで入る
	visitor := apiclient.New(st.apiURL)
	me, err := visitor.CreateGuest(ctx, "たいち🐹", 1)
	if err != nil {
		t.Fatal(err)
	}
	join, err := visitor.Join(ctx, ws.InviteCode)
	if err != nil {
		t.Fatal(err)
	}
	st.boundRoom = join.Room.ID // この GameServer は、このルームを担当する

	m, _ := firstMessage(t, join.ServerURL, join.RoomToken, "たいち🐹")
	w := m.GetWelcome()
	if w == nil {
		t.Fatalf("want Welcome, got %v", m)
	}
	// GameServer 上のプレイヤー ID は、API のプレイヤー ID（トークンの sub）になる。名前を偽って別人にはなれない
	if w.GetPlayerId() != me.ID {
		t.Fatalf("player id on the game server = %q, want the API's %q", w.GetPlayerId(), me.ID)
	}
	if w.GetPlayers()[0].GetProfile().GetDisplayName() != "たいち🐹" {
		t.Fatalf("profile = %v", w.GetPlayers()[0].GetProfile())
	}
}

func TestSamePlayerJoiningTwiceReplacesTheOldConnection(t *testing.T) {
	st := startStack(t)
	ctx := context.Background()

	cl := apiclient.New(st.apiURL)
	if _, err := cl.CreateGuest(ctx, "p", 0); err != nil {
		t.Fatal(err)
	}
	ws, _ := cl.CreateWorkspace(ctx, "w")

	j1, err := cl.Join(ctx, ws.InviteCode)
	if err != nil {
		t.Fatal(err)
	}
	_, first := firstMessage(t, j1.ServerURL, j1.RoomToken, "tab-1")

	// 同じプレイヤーが別のタブで入り直す
	j2, _ := cl.Join(ctx, ws.InviteCode)
	m, _ := firstMessage(t, j2.ServerURL, j2.RoomToken, "tab-2")
	if m.GetWelcome() == nil || len(m.GetWelcome().GetPlayers()) != 1 {
		t.Fatalf("the player must appear once, got %v", m)
	}

	// 古いタブの接続は閉じられる
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-first.Recv():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the old connection must be closed")
		}
	}
}

func TestGameServerRejectsBadTokens(t *testing.T) {
	st := startStack(t)
	ctx := context.Background()

	cl := apiclient.New(st.apiURL)
	if _, err := cl.CreateGuest(ctx, "p", 0); err != nil {
		t.Fatal(err)
	}
	ws, _ := cl.CreateWorkspace(ctx, "w")
	join, _ := cl.Join(ctx, ws.InviteCode)
	st.boundRoom = join.Room.ID

	// プレイヤートークン（API の認証用）は、ルームのトークンとしては使えない
	cases := map[string]string{
		"empty":                "",
		"garbage":              "not-a-token",
		"a player token":       cl.Token(),
		"tampered room token":  join.RoomToken[:len(join.RoomToken)-3] + "AAA",
		"token for no room":    forgedRoomToken(t, "someone", ""),
		"another key's token":  otherKeyToken(t),
		"another room's token": otherRoomToken(t),
	}
	for name, tok := range cases {
		m, _ := firstMessage(t, join.ServerURL, tok, "evil")
		if m.GetError().GetCode() != officev1.ErrorCode_ERROR_CODE_BAD_TOKEN {
			t.Errorf("%s: want BAD_TOKEN, got %v", name, m)
		}
	}

	// 正しいトークンは通る（上の拒否が、設定の誤りによるものではないことの確認）
	if m, _ := firstMessage(t, join.ServerURL, join.RoomToken, "ok"); m.GetWelcome() == nil {
		t.Fatalf("a valid token must be accepted, got %v", m)
	}
}

func forgedRoomToken(t *testing.T, player, room string) string {
	t.Helper()
	s, _ := auth.NewSigner(signKeyPlayer, signKeyRoom)
	tok, _, err := s.IssueRoomToken(auth.RoomClaims{PlayerID: player, RoomID: room})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func otherKeyToken(t *testing.T) string {
	t.Helper()
	s, _ := auth.NewSigner(signKeyPlayer, "a-completely-different-room-secret-0123456789")
	tok, _, err := s.IssueRoomToken(auth.RoomClaims{PlayerID: "p", RoomID: "r"})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func otherRoomToken(t *testing.T) string {
	t.Helper()
	return forgedRoomToken(t, "p", "01JZZZZZZZZZZZZZZZZZZZZZZZ")
}
