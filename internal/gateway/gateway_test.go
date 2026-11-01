package gateway_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	officev1 "github.com/o-ga09/gopher-office/gen/office/v1"
	"github.com/o-ga09/gopher-office/internal/auth"
	"github.com/o-ga09/gopher-office/internal/gameserver"
	"github.com/o-ga09/gopher-office/internal/gateway"
	"github.com/o-ga09/gopher-office/internal/k8s"
	"github.com/o-ga09/gopher-office/internal/netcode"
)

const (
	playerSecret = "gw-test-player-secret-0123456789-012345"
	roomSecret   = "gw-test-room-secret-0123456789-0123456"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type staticResolver map[string]string

func (r staticResolver) Resolve(_ context.Context, gs string) (string, error) {
	if a, ok := r[gs]; ok {
		return a, nil
	}
	return "", gateway.ErrUnavailable
}

// startStack は GameServer（実物）とゲートウェイを起動し、ゲートウェイの WebSocket URL（token なし）を返す。
func startStack(t *testing.T) (gwURL string, signer *auth.Signer) {
	t.Helper()
	signer, err := auth.NewSigner(playerSecret, roomSecret)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := auth.NewRoomVerifier(roomSecret, func() string { return "room-1" })
	if err != nil {
		t.Fatal(err)
	}

	gs := gameserver.NewServer(gameserver.Config{RoomID: "room-1", Verifier: verifier, Logger: quiet})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { gs.Room().Run(ctx); close(done) }()
	gsSrv := httptest.NewServer(gs.Handler())

	gwVerifier, err := auth.NewRoomVerifier(roomSecret, nil)
	if err != nil {
		t.Fatal(err)
	}
	g, err := gateway.New(gateway.Config{
		Verifier: gwVerifier,
		Resolver: staticResolver{"gs-1": strings.TrimPrefix(gsSrv.URL, "http://")},
		Logger:   quiet,
	})
	if err != nil {
		t.Fatal(err)
	}
	gwSrv := httptest.NewServer(g.Handler())
	t.Cleanup(func() {
		gwSrv.Close()
		gsSrv.Close()
		cancel()
		<-done
	})
	return "ws" + strings.TrimPrefix(gwSrv.URL, "http") + "/room", signer
}

func roomToken(t *testing.T, s *auth.Signer, player, room, gs string) string {
	t.Helper()
	tok, _, err := s.IssueRoomToken(auth.RoomClaims{PlayerID: player, RoomID: room, GameServer: gs})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func firstMessage(t *testing.T, url, token string) *officev1.ServerMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := netcode.Dial(ctx, url, &officev1.Hello{RoomToken: token, Profile: &officev1.Profile{DisplayName: "tester"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	select {
	case m, ok := <-c.Recv():
		if !ok {
			t.Fatalf("closed before any message: %v", c.Err())
		}
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
		return nil
	}
}

func TestRelaysTheRoomThroughTheGateway(t *testing.T) {
	url, signer := startStack(t)
	tok := roomToken(t, signer, "player-1", "room-1", "gs-1")

	m := firstMessage(t, url+"?token="+tok, tok)
	w := m.GetWelcome()
	if w == nil || w.GetPlayerId() != "player-1" || len(w.GetPlayers()) != 1 {
		t.Fatalf("want Welcome for player-1 via the gateway, got %v", m)
	}
}

// ゲートウェイを通っても、別のルームのトークンは GameServer が拒否する（多重の検証）。
func TestGameServerStillChecksTheRoom(t *testing.T) {
	url, signer := startStack(t)
	tok := roomToken(t, signer, "player-1", "other-room", "gs-1")

	m := firstMessage(t, url+"?token="+tok, tok)
	if e := m.GetError(); e == nil || e.GetCode() != officev1.ErrorCode_ERROR_CODE_BAD_TOKEN {
		t.Fatalf("want BadToken from the game server, got %v", m)
	}
}

func TestRejectsBadTokensBeforeConnecting(t *testing.T) {
	url, signer := startStack(t)
	httpURL := "http" + strings.TrimPrefix(url, "ws")

	otherSigner, _ := auth.NewSigner(playerSecret, "another-room-secret-0123456789-0123456")
	playerTok, _ := signer.IssuePlayerToken("p")
	cases := map[string]struct {
		query string
		want  int
	}{
		"no token":          {"", http.StatusUnauthorized},
		"garbage":           {"?token=abc", http.StatusUnauthorized},
		"other key":         {"?token=" + roomToken(t, otherSigner, "p", "room-1", "gs-1"), http.StatusUnauthorized},
		"a playerToken":     {"?token=" + playerTok, http.StatusUnauthorized},
		"unknown gs":        {"?token=" + roomToken(t, signer, "p", "room-1", "gs-404"), http.StatusServiceUnavailable},
		"path-like gs name": {"?token=" + roomToken(t, signer, "p", "room-1", "../../secrets"), http.StatusServiceUnavailable},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			resp, err := http.Get(httpURL + c.query)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != c.want {
				t.Fatalf("status %d, want %d", resp.StatusCode, c.want)
			}
		})
	}
}

func TestNewRequiresDependencies(t *testing.T) {
	if _, err := gateway.New(gateway.Config{}); err == nil {
		t.Fatal("Verifier and Resolver are required")
	}
}

func TestKubeResolver(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/api/v1/namespaces/gopher-office/pods/gs-run":
			_, _ = io.WriteString(w, `{"status":{"phase":"Running","podIP":"10.42.0.7"}}`)
		case "/api/v1/namespaces/gopher-office/pods/gs-pending":
			_, _ = io.WriteString(w, `{"status":{"phase":"Pending"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	r := &gateway.KubeResolver{Kube: k8s.New(ts.URL), Namespace: "gopher-office", Port: "7654"}
	ctx := context.Background()

	if a, err := r.Resolve(ctx, "gs-run"); err != nil || a != "10.42.0.7:7654" {
		t.Fatalf("addr = %q err=%v", a, err)
	}
	if _, err := r.Resolve(ctx, "gs-run"); err != nil || calls != 1 {
		t.Fatalf("the second lookup must be cached: calls=%d err=%v", calls, err)
	}
	for _, name := range []string{"gs-pending", "gs-missing", "Bad_Name", "../x", ""} {
		if _, err := r.Resolve(ctx, name); !errors.Is(err, gateway.ErrUnavailable) {
			t.Errorf("%q: err = %v, want ErrUnavailable", name, err)
		}
	}
}
