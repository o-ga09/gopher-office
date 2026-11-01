// Package gateway は、クライアントの WebSocket を、roomToken が指す GameServer に中継する。
//
// GameServer は Pod の IP でしか届かない（ポートを外に公開しない）ので、外部からは必ずここを通る。
// ゲートウェイは roomToken の署名と期限だけを確認して接続先を決め、中身のフレームは解釈しない。
// ルームの一致や満員の判定は GameServer が行う。
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/o-ga09/gopher-office/internal/auth"
	"github.com/o-ga09/gopher-office/internal/k8s"
)

const (
	dialTimeout = 5 * time.Second
	// クライアント → サーバのフレームは数十バイト。GameServer と同じ上限にする。
	maxClientFrame = 4 << 10
	// サーバ → クライアントは、200 人の Welcome などで大きくなる。
	maxServerFrame = 1 << 20

	defaultMaxConns = 5000
	podCacheTTL     = 10 * time.Second
)

// Resolver は GameServer の名前から、接続先（host:port）を求める。
type Resolver interface {
	Resolve(ctx context.Context, gameServer string) (hostPort string, err error)
}

// ErrUnavailable は、GameServer が見つからない、または接続できる状態でないことを表す。
var ErrUnavailable = errors.New("gateway: game server is unavailable")

// Config はゲートウェイの設定。
type Config struct {
	Verifier *auth.RoomVerifier
	Resolver Resolver
	Logger   *slog.Logger
	// OriginPatterns はクライアントの WebSocket で許可する Origin（host のパターン）。空なら同一オリジンのみ。
	OriginPatterns []string
	// MaxConns は同時に中継する接続数の上限。0 なら既定値。
	MaxConns int
}

// Gateway は http.Handler を持つ。
type Gateway struct {
	cfg    Config
	log    *slog.Logger
	active atomic.Int64
}

// New はゲートウェイを作る。
func New(cfg Config) (*Gateway, error) {
	if cfg.Verifier == nil || cfg.Resolver == nil {
		return nil, errors.New("gateway: Verifier and Resolver are required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = defaultMaxConns
	}
	return &Gateway{cfg: cfg, log: cfg.Logger}, nil
}

// Active は中継中の接続数。
func (g *Gateway) Active() int { return int(g.active.Load()) }

// Handler は /room（WebSocket の中継）と /healthz を持つ。
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/room", g.handleRoom)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func (g *Gateway) handleRoom(w http.ResponseWriter, r *http.Request) {
	// トークンは URL に載る（WebSocket はヘッダを付けられない）。有効期間は 60 秒で、ログには URL を出さない
	claims, err := g.cfg.Verifier.Claims(r.URL.Query().Get("token"))
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	if g.active.Load() >= int64(g.cfg.MaxConns) {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}

	addr, err := g.cfg.Resolver.Resolve(r.Context(), claims.GameServer)
	if err != nil {
		g.log.Warn("resolve failed", "gs", claims.GameServer, "err", err)
		http.Error(w, "room is unavailable", http.StatusServiceUnavailable)
		return
	}

	dctx, cancel := context.WithTimeout(r.Context(), dialTimeout)
	up, _, err := websocket.Dial(dctx, "ws://"+addr+"/room", nil)
	cancel()
	if err != nil {
		g.log.Warn("dial game server failed", "gs", claims.GameServer, "err", err)
		http.Error(w, "room is unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = up.CloseNow() }()
	up.SetReadLimit(maxServerFrame)

	client, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: g.cfg.OriginPatterns})
	if err != nil {
		return // Accept がレスポンスを返している
	}
	defer func() { _ = client.CloseNow() }()
	client.SetReadLimit(maxClientFrame)

	g.active.Add(1)
	defer g.active.Add(-1)

	ctx, stop := context.WithCancel(r.Context())
	defer stop()
	var once sync.Once
	finish := func(from string, err error) {
		once.Do(func() {
			g.log.Debug("relay ended", "side", from, "err", err)
			stop()
		})
	}
	go func() { finish("client", pipe(ctx, client, up)) }()
	go func() { finish("server", pipe(ctx, up, client)) }()
	<-ctx.Done()

	// 片方が閉じたら、もう片方にも伝える（理由は GameServer が付けたものをそのまま渡せないので、定型にする）
	_ = client.Close(websocket.StatusNormalClosure, "")
	_ = up.Close(websocket.StatusNormalClosure, "")
}

// pipe は src から読んだフレームを、種別を保ったまま dst に書く。どちらかが閉じる・失敗すると戻る。
func pipe(ctx context.Context, src, dst *websocket.Conn) error {
	for {
		typ, data, err := src.Read(ctx)
		if err != nil {
			return err
		}
		if err := dst.Write(ctx, typ, data); err != nil {
			return err
		}
	}
}

// --- Kubernetes（Pod の IP の解決）---

// gameServerName は Pod 名（DNS ラベル）として正しいものだけを通す。トークンの値を API のパスに入れるため。
var gameServerName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// KubeResolver は GameServer の Pod の IP を、k8s API から求める。GameServer の Pod 名は GameServer の名前と同じ。
// 結果は短時間キャッシュする（入室が集中しても、k8s API を叩きすぎないように）。
type KubeResolver struct {
	Kube      *k8s.Client
	Namespace string
	Port      string // GameServer のポート（例: 7654）

	mu    sync.Mutex
	cache map[string]cachedAddr
	now   func() time.Time
}

type cachedAddr struct {
	addr    string
	expires time.Time
}

// Resolve は gameServer の Pod の host:port を返す。
func (k *KubeResolver) Resolve(ctx context.Context, gs string) (string, error) {
	if !gameServerName.MatchString(gs) {
		return "", fmt.Errorf("%w: bad name", ErrUnavailable)
	}
	now := time.Now
	if k.now != nil {
		now = k.now
	}

	k.mu.Lock()
	if c, ok := k.cache[gs]; ok && now().Before(c.expires) {
		k.mu.Unlock()
		return c.addr, nil
	}
	k.mu.Unlock()

	var pod struct {
		Status struct {
			Phase string `json:"phase"`
			PodIP string `json:"podIP"`
		} `json:"status"`
	}
	path := "/api/v1/namespaces/" + url.PathEscape(k.Namespace) + "/pods/" + gs
	switch err := k.Kube.Do(ctx, http.MethodGet, path, nil, &pod); {
	case errors.Is(err, k8s.ErrNotFound):
		return "", fmt.Errorf("%w: pod not found", ErrUnavailable)
	case err != nil:
		return "", err
	}
	if pod.Status.Phase != "Running" || net.ParseIP(pod.Status.PodIP) == nil {
		return "", fmt.Errorf("%w: pod is %q", ErrUnavailable, strings.ToLower(pod.Status.Phase))
	}
	addr := net.JoinHostPort(pod.Status.PodIP, k.Port)

	k.mu.Lock()
	if k.cache == nil {
		k.cache = make(map[string]cachedAddr)
	}
	k.cache[gs] = cachedAddr{addr: addr, expires: now().Add(podCacheTTL)}
	k.mu.Unlock()
	return addr, nil
}
