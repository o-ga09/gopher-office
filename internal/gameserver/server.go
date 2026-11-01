package gameserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"

	officev1 "github.com/o-ga09/gopher-office/gen/office/v1"
	"github.com/o-ga09/gopher-office/internal/icon"
	"github.com/o-ga09/gopher-office/internal/profile"
	"github.com/o-ga09/gopher-office/internal/wire"
)

const (
	helloTimeout = 10 * time.Second
	writeTimeout = 5 * time.Second
	// maxFrameBytes はクライアントからの 1 フレームの上限。入力メッセージは数十バイトしかない。
	maxFrameBytes = 4 << 10
	// 受信メッセージのレート制限（1 接続あたり）。移動入力は数 Hz なので十分な余裕がある。
	rateLimitPerSec = 60
	rateBurst       = 120
)

// TokenVerifier は roomToken を検証し、プレイヤー ID を返す。
// 空の ID を返した場合は、サーバがランダムな ID を割り当てる（ローカル開発用）。
type TokenVerifier interface {
	Verify(token string) (playerID string, err error)
}

// InsecureVerifier はトークンを検証しない（-local 用）。本番では使わない。
type InsecureVerifier struct{}

// Verify は常に成功し、ID の割り当てをサーバに任せる。
func (InsecureVerifier) Verify(string) (string, error) { return "", nil }

// Server は Room を WebSocket で公開する。
type Server struct {
	room *Room
	cfg  Config
	log  *slog.Logger
}

// NewServer は Room と、それを公開する HTTP ハンドラを作る。
func NewServer(cfg Config) *Server {
	room := NewRoom(cfg)
	return &Server{room: room, cfg: room.cfg, log: room.log}
}

// Room は内部のルームを返す。
func (s *Server) Room() *Room { return s.room }

// Handler は /room（WebSocket）と /healthz を持つハンドラを返す。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/room", s.handleRoom)
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		s.room.WriteMetrics(w)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func (s *Server) handleRoom(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.cfg.OriginPatterns})
	if err != nil {
		return // Accept がすでにエラーレスポンスを返している
	}
	defer func() { _ = ws.CloseNow() }()
	ws.SetReadLimit(maxFrameBytes)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	c, ok := s.handshake(ctx, ws)
	if !ok {
		return
	}
	c.closeWS = func(reason string) { _ = ws.Close(websocket.StatusPolicyViolation, truncate(reason, 100)) }

	// 入室。ルームが止まっているか満員なら終了する
	reply := make(chan joinResult, 1)
	if !s.room.submit(ctx, joinCmd{c: c, reply: reply}) {
		s.reject(ctx, ws, officev1.ErrorCode_ERROR_CODE_UNSPECIFIED, "room is closed")
		return
	}
	var res joinResult
	select {
	case res = <-reply:
	case <-ctx.Done():
		return
	}
	if res.full {
		s.reject(ctx, ws, officev1.ErrorCode_ERROR_CODE_FULL, "room is full")
		return
	}
	defer func() {
		c.shutdown("")
		s.room.submit(context.Background(), leaveCmd{c: c})
	}()

	go s.writeLoop(ctx, ws, c, cancel)
	s.readLoop(ctx, ws, c)
}

// handshake は最初の Hello を読んで検証し、接続を作る。
func (s *Server) handshake(ctx context.Context, ws *websocket.Conn) (*conn, bool) {
	hctx, cancel := context.WithTimeout(ctx, helloTimeout)
	defer cancel()

	_, data, err := ws.Read(hctx)
	if err != nil {
		return nil, false
	}
	msg, err := wire.UnmarshalClient(data)
	hello := msg.GetHello()
	if err != nil || hello == nil {
		s.reject(ctx, ws, officev1.ErrorCode_ERROR_CODE_BAD_REQUEST, "expected hello")
		return nil, false
	}

	if hello.GetClientVersion() != wire.ProtocolVersion {
		s.log.Info("rejected: protocol version mismatch", "client", hello.GetClientVersion(), "server", wire.ProtocolVersion)
		s.reject(ctx, ws, officev1.ErrorCode_ERROR_CODE_VERSION_MISMATCH, "client version mismatch; please reload the page")
		return nil, false
	}

	id, err := s.cfg.Verifier.Verify(hello.GetRoomToken())
	if err != nil {
		s.log.Info("rejected: bad token", "err", err)
		s.reject(ctx, ws, officev1.ErrorCode_ERROR_CODE_BAD_TOKEN, "invalid token")
		return nil, false
	}
	if id == "" {
		id = newPlayerID()
	}

	p := &officev1.Profile{
		DisplayName: profile.CleanName(hello.GetProfile().GetDisplayName()),
		Gender:      wire.GenderToProto(wire.GenderFromProto(hello.GetProfile().GetGender())),
		IconHash:    validIconHash(hello.GetProfile().GetIconHash()),
	}
	return newConn(id, p), true
}

// reject はエラーを送って接続を閉じる。
func (s *Server) reject(ctx context.Context, ws *websocket.Conn, code officev1.ErrorCode, message string) {
	if b, err := wire.Marshal(&officev1.ServerMessage{Body: &officev1.ServerMessage_Error{Error: &officev1.Error{Code: code, Message: message}}}); err == nil {
		wctx, cancel := context.WithTimeout(ctx, writeTimeout)
		_ = ws.Write(wctx, websocket.MessageBinary, b)
		cancel()
	}
	_ = ws.Close(websocket.StatusPolicyViolation, truncate(message, 100))
}

func (s *Server) writeLoop(ctx context.Context, ws *websocket.Conn, c *conn, cancel context.CancelFunc) {
	defer cancel()
	for {
		select {
		case b := <-c.out:
			wctx, wcancel := context.WithTimeout(ctx, writeTimeout)
			err := ws.Write(wctx, websocket.MessageBinary, b)
			wcancel()
			if err != nil {
				return
			}
			s.room.m.bytesSent.Add(int64(len(b)))
			s.room.m.msgsSent.Add(1)
		case <-c.done:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) readLoop(ctx context.Context, ws *websocket.Conn, c *conn) {
	limiter := newRateLimiter(rateLimitPerSec, rateBurst)
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) && websocket.CloseStatus(err) == -1 {
				s.log.Debug("read ended", "player", c.id, "err", err)
			}
			return
		}
		if !limiter.allow(time.Now()) {
			s.reject(ctx, ws, officev1.ErrorCode_ERROR_CODE_RATE_LIMITED, "too many messages")
			return
		}
		msg, err := wire.UnmarshalClient(data)
		if err != nil {
			s.reject(ctx, ws, officev1.ErrorCode_ERROR_CODE_BAD_REQUEST, "malformed message")
			return
		}
		switch b := msg.GetBody().(type) {
		case *officev1.ClientMessage_Ping:
			pong, err := wire.Marshal(&officev1.ServerMessage{Body: &officev1.ServerMessage_Pong{Pong: &officev1.Pong{
				ClientTimeMs: b.Ping.GetClientTimeMs(),
				ServerTick:   s.room.serverTick.Load(),
			}}})
			if err == nil {
				select {
				case c.out <- pong:
				default: // 混んでいるときの Pong は捨ててよい
				}
			}
		case *officev1.ClientMessage_MoveTo, *officev1.ClientMessage_MoveDir:
			if !s.room.submit(ctx, inputCmd{c: c, msg: msg}) {
				return
			}
		}
	}
}

// validIconHash は、アイコンのハッシュとして正しい形式（小文字 16 進数 64 文字）のものだけを通す。
// 他のクライアントの URL 組み立てに使われるので、任意の文字列は載せない。
func validIconHash(s string) string {
	if icon.ValidHash(s) {
		return s
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// rateLimiter は単純なトークンバケット。1 接続の reader goroutine だけが使う。
type rateLimiter struct {
	rate, burst float64
	tokens      float64
	last        time.Time
}

func newRateLimiter(ratePerSec, burst int) *rateLimiter {
	return &rateLimiter{rate: float64(ratePerSec), burst: float64(burst), tokens: float64(burst)}
}

func (l *rateLimiter) allow(now time.Time) bool {
	if !l.last.IsZero() {
		l.tokens = min(l.burst, l.tokens+now.Sub(l.last).Seconds()*l.rate)
	}
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}
