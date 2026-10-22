package gameserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	officev1 "github.com/o-ga09/gopher-office/gen/office/v1"
	"github.com/o-ga09/gopher-office/internal/wire"
)

type testClient struct {
	t  *testing.T
	ws *websocket.Conn
}

func startServer(t *testing.T, cfg Config) (url string) {
	t.Helper()
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	if cfg.Verifier == nil {
		cfg.Verifier = InsecureVerifier{}
	}
	s := NewServer(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Room().Run(ctx); close(done) }()
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		ts.Close()
		cancel()
		<-done
	})
	return "ws" + strings.TrimPrefix(ts.URL, "http") + "/room"
}

func dial(t *testing.T, url string) *testClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, url, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	return &testClient{t: t, ws: ws}
}

func (c *testClient) send(m *officev1.ClientMessage) {
	c.t.Helper()
	b, err := wire.Marshal(m)
	if err != nil {
		c.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageBinary, b); err != nil {
		c.t.Fatal(err)
	}
}

func (c *testClient) hello(token, name string, gender officev1.Gender) {
	c.send(&officev1.ClientMessage{Body: &officev1.ClientMessage_Hello{Hello: &officev1.Hello{
		RoomToken: token,
		Profile:   &officev1.Profile{DisplayName: name, Gender: gender},
	}}})
}

func (c *testClient) recv() (*officev1.ServerMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, b, err := c.ws.Read(ctx)
	if err != nil {
		return nil, err
	}
	return wire.UnmarshalServer(b)
}

// recvUntil は条件に合うメッセージが来るまで読み進める。
func (c *testClient) recvUntil(pred func(*officev1.ServerMessage) bool) *officev1.ServerMessage {
	c.t.Helper()
	for {
		m, err := c.recv()
		if err != nil {
			c.t.Fatalf("waiting for a message: %v", err)
		}
		if pred(m) {
			return m
		}
	}
}

func isWelcome(m *officev1.ServerMessage) bool { return m.GetWelcome() != nil }

func TestTwoClientsSeeEachOtherMove(t *testing.T) {
	url := startServer(t, Config{MaxPlayers: 10})

	a := dial(t, url)
	a.hello("", "alice", officev1.Gender_GENDER_FEMALE)
	wa := a.recvUntil(isWelcome).GetWelcome()

	b := dial(t, url)
	b.hello("", "bob", officev1.Gender_GENDER_MALE)
	wb := b.recvUntil(isWelcome).GetWelcome()
	if wa.GetPlayerId() == wb.GetPlayerId() {
		t.Fatal("players must get distinct IDs")
	}
	if len(wb.GetPlayers()) != 2 {
		t.Fatalf("b must see both players, got %d", len(wb.GetPlayers()))
	}

	joined := a.recvUntil(func(m *officev1.ServerMessage) bool { return m.GetPlayerJoined() != nil }).GetPlayerJoined()
	if joined.GetPlayer().GetId() != wb.GetPlayerId() || joined.GetPlayer().GetProfile().GetDisplayName() != "bob" {
		t.Fatalf("unexpected PlayerJoined: %v", joined)
	}

	a.send(&officev1.ClientMessage{Body: &officev1.ClientMessage_MoveTo{MoveTo: &officev1.MoveTo{X: 2, Y: 2}}})
	moved := b.recvUntil(func(m *officev1.ServerMessage) bool { return m.GetMoved() != nil }).GetMoved()
	s := moved.GetStates()[0]
	if s.GetPlayerId() != wa.GetPlayerId() || !s.GetHasTarget() || s.GetTargetX() != 2 || s.GetTargetY() != 2 {
		t.Fatalf("b must see a's move, got %v", s)
	}

	// 切断すると、残った側に PlayerLeft が届く
	_ = a.ws.Close(websocket.StatusNormalClosure, "bye")
	left := b.recvUntil(func(m *officev1.ServerMessage) bool { return m.GetPlayerLeft() != nil }).GetPlayerLeft()
	if left.GetPlayerId() != wa.GetPlayerId() {
		t.Fatalf("unexpected PlayerLeft: %v", left)
	}
}

func TestProfileIsSanitized(t *testing.T) {
	url := startServer(t, Config{})
	c := dial(t, url)
	c.hello("", "a\nb\u202ec", officev1.Gender(99))
	w := c.recvUntil(isWelcome).GetWelcome()
	p := w.GetPlayers()[0].GetProfile()
	if p.GetDisplayName() != "abc" {
		t.Fatalf("name = %q, want %q", p.GetDisplayName(), "abc")
	}
	if p.GetGender() != officev1.Gender_GENDER_OTHER {
		t.Fatalf("unknown gender must become other, got %v", p.GetGender())
	}
}

func expectError(t *testing.T, c *testClient, want officev1.ErrorCode) {
	t.Helper()
	m := c.recvUntil(func(m *officev1.ServerMessage) bool { return m.GetError() != nil })
	if got := m.GetError().GetCode(); got != want {
		t.Fatalf("error code = %v, want %v", got, want)
	}
	// エラーの後は接続が閉じられる
	if _, err := c.recv(); err == nil {
		t.Fatal("connection must be closed after an error")
	}
}

func TestFirstMessageMustBeHello(t *testing.T) {
	url := startServer(t, Config{})
	c := dial(t, url)
	c.send(&officev1.ClientMessage{Body: &officev1.ClientMessage_Ping{Ping: &officev1.Ping{}}})
	expectError(t, c, officev1.ErrorCode_ERROR_CODE_BAD_REQUEST)
}

func TestGarbageIsRejected(t *testing.T) {
	url := startServer(t, Config{})
	c := dial(t, url)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageBinary, []byte{0xff, 0xff, 0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	expectError(t, c, officev1.ErrorCode_ERROR_CODE_BAD_REQUEST)
}

type denyVerifier struct{}

func (denyVerifier) Verify(string) (string, error) { return "", errors.New("nope") }

func TestBadTokenIsRejected(t *testing.T) {
	url := startServer(t, Config{Verifier: denyVerifier{}})
	c := dial(t, url)
	c.hello("forged", "mallory", officev1.Gender_GENDER_OTHER)
	expectError(t, c, officev1.ErrorCode_ERROR_CODE_BAD_TOKEN)
}

type fixedVerifier struct{ id string }

func (v fixedVerifier) Verify(string) (string, error) { return v.id, nil }

func TestVerifierProvidesThePlayerID(t *testing.T) {
	url := startServer(t, Config{Verifier: fixedVerifier{id: "player-42"}})
	c := dial(t, url)
	c.hello("tok", "x", officev1.Gender_GENDER_MALE)
	if got := c.recvUntil(isWelcome).GetWelcome().GetPlayerId(); got != "player-42" {
		t.Fatalf("player id = %q", got)
	}
}

func TestFullRoomIsRejected(t *testing.T) {
	url := startServer(t, Config{MaxPlayers: 1})
	a := dial(t, url)
	a.hello("", "a", officev1.Gender_GENDER_MALE)
	a.recvUntil(isWelcome)

	b := dial(t, url)
	b.hello("", "b", officev1.Gender_GENDER_MALE)
	expectError(t, b, officev1.ErrorCode_ERROR_CODE_FULL)
}

func TestPingGetsPong(t *testing.T) {
	url := startServer(t, Config{})
	c := dial(t, url)
	c.hello("", "p", officev1.Gender_GENDER_MALE)
	c.recvUntil(isWelcome)
	c.send(&officev1.ClientMessage{Body: &officev1.ClientMessage_Ping{Ping: &officev1.Ping{ClientTimeMs: 12345}}})
	pong := c.recvUntil(func(m *officev1.ServerMessage) bool { return m.GetPong() != nil }).GetPong()
	if pong.GetClientTimeMs() != 12345 {
		t.Fatalf("pong must echo the client time, got %d", pong.GetClientTimeMs())
	}
}

func TestFloodingIsRateLimited(t *testing.T) {
	url := startServer(t, Config{})
	c := dial(t, url)
	c.hello("", "spam", officev1.Gender_GENDER_MALE)
	c.recvUntil(isWelcome)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, _ := wire.Marshal(&officev1.ClientMessage{Body: &officev1.ClientMessage_Ping{Ping: &officev1.Ping{}}})
	for i := 0; i < rateBurst*3; i++ {
		if err := c.ws.Write(ctx, websocket.MessageBinary, b); err != nil {
			break // すでに切断された
		}
	}
	m := c.recvUntil(func(m *officev1.ServerMessage) bool { return m.GetError() != nil })
	if m.GetError().GetCode() != officev1.ErrorCode_ERROR_CODE_RATE_LIMITED {
		t.Fatalf("error code = %v", m.GetError().GetCode())
	}
}
