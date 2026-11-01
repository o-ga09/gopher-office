package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/o-ga09/gopher-office/internal/auth"
	"github.com/o-ga09/gopher-office/internal/icon"
)

const (
	// maxBodyBytes は JSON リクエストのボディの上限。このサーバが受け取る JSON は数百バイトしかない。
	maxBodyBytes = 8 << 10

	defaultMaxWorkspacesPerPlayer = 5
	defaultGuestPerMinute         = 20
	defaultActionPerMinute        = 60
)

// Config は API サーバの設定。
type Config struct {
	Store     Store
	Signer    *auth.Signer
	Allocator RoomAllocator
	Logger    *slog.Logger

	// MaxWorkspacesPerPlayer は 1 人が作れるワークスペースの数。0 なら既定値。
	MaxWorkspacesPerPlayer int
	// GuestPerMinute は IP ごとに、ゲストを作れる回数（分あたり）。0 なら既定値。
	GuestPerMinute int
	// ActionPerMinute はプレイヤーごとに、ワークスペースの作成・入室ができる回数（分あたり）。0 なら既定値。
	ActionPerMinute int
	// TrustCloudflareIP を true にすると、クライアントの IP を CF-Connecting-IP ヘッダから取る。
	// Cloudflare Tunnel の背後で、かつ API に Cloudflare を通らずに直接届かない場合にだけ使う。
	TrustCloudflareIP bool
	// CORSOrigins は、ブラウザから別のオリジンで呼ぶことを許可するオリジン（例: http://localhost:7654）。
	// 本番は同じオリジンで配信するので空にする（CORS を無効にする）。
	CORSOrigins []string
}

type handler struct {
	cfg Config
	log *slog.Logger
}

// NewServer は API の http.Handler を作る。
func NewServer(cfg Config) (*echo.Echo, error) {
	switch {
	case cfg.Store == nil:
		return nil, errors.New("api: Store is required")
	case cfg.Signer == nil:
		return nil, errors.New("api: Signer is required")
	case cfg.Allocator == nil:
		return nil, errors.New("api: Allocator is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.MaxWorkspacesPerPlayer <= 0 {
		cfg.MaxWorkspacesPerPlayer = defaultMaxWorkspacesPerPlayer
	}
	if cfg.GuestPerMinute <= 0 {
		cfg.GuestPerMinute = defaultGuestPerMinute
	}
	if cfg.ActionPerMinute <= 0 {
		cfg.ActionPerMinute = defaultActionPerMinute
	}
	h := &handler{cfg: cfg, log: cfg.Logger}

	e := echo.New()
	e.Logger = cfg.Logger
	e.Validator = newValidator()
	e.HTTPErrorHandler = h.handleError
	e.IPExtractor = echo.ExtractIPDirect()
	if cfg.TrustCloudflareIP {
		e.IPExtractor = cloudflareIP
	}

	e.Use(middleware.Recover())
	if len(cfg.CORSOrigins) > 0 {
		e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
			AllowOrigins: cfg.CORSOrigins,
			AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodOptions},
			AllowHeaders: []string{"Authorization", "Content-Type"},
			MaxAge:       600,
		}))
	}
	// JSON は 8 KB まで。アイコンのアップロードだけは、ルーム側のミドルウェアで 256 KB に緩める
	e.Use(middleware.BodyLimitWithConfig(middleware.BodyLimitConfig{
		LimitBytes: maxBodyBytes,
		Skipper:    func(c *echo.Context) bool { return c.Path() == iconUploadPath },
	}))
	e.Use(h.requestLog())

	e.GET("/healthz", h.healthz)

	v1 := e.Group("/v1")
	v1.POST("/guest", h.createGuest, ipLimiter(cfg.GuestPerMinute))
	v1.GET("/workspaces/by-code/:code", h.getWorkspaceByCode)
	v1.GET("/players/:id/icon", h.getIcon)

	// 認証はルートごとに付ける。グループに付けると、存在しないパスにも認証が掛かり、404 が 401 になる
	limit := playerLimiter(cfg.ActionPerMinute)
	v1.GET("/me", h.getMe, h.requireAuth)
	v1.PATCH("/me", h.updateMe, h.requireAuth)
	v1.PUT("/me/icon", h.putIcon, h.requireAuth, limit, middleware.BodyLimit(icon.MaxUploadBytes))
	v1.POST("/workspaces", h.createWorkspace, h.requireAuth, limit)
	v1.POST("/workspaces/:code/join", h.joinWorkspace, h.requireAuth, limit)

	return e, nil
}

func (h *handler) healthz(c *echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
	defer cancel()
	if err := h.cfg.Store.Ping(ctx); err != nil {
		h.log.Error("health check failed", "err", err)
		return c.String(http.StatusServiceUnavailable, "unavailable")
	}
	return c.String(http.StatusOK, "ok")
}

// requestLog はアクセスログを出す。招待コードは URL に含まれる機密に近い値なので、実際の URI ではなく
// ルートのパターン（/v1/workspaces/:code/join）を記録する。ヘッダとボディは記録しない。
func (h *handler) requestLog() echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogMethod:    true,
		LogRoutePath: true,
		LogStatus:    true,
		LogLatency:   true,
		LogValuesFunc: func(_ *echo.Context, v middleware.RequestLoggerValues) error {
			h.log.Info("request", "method", v.Method, "route", v.RoutePath, "status", v.Status, "latency", v.Latency)
			return nil
		},
	})
}

func cloudflareIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	return echo.ExtractIPDirect()(r)
}

// limiter は、識別子ごとに 1 分あたり perMinute 回までに制限するミドルウェアを作る。
func limiter(perMinute int, id func(*echo.Context) (string, error)) echo.MiddlewareFunc {
	store := middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
		Rate:      float64(perMinute) / 60,
		Burst:     perMinute,
		ExpiresIn: 10 * time.Minute,
	})
	return middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Store:               store,
		IdentifierExtractor: id,
		DenyHandler: func(*echo.Context, string, error) error {
			return newAPIError(http.StatusTooManyRequests, "rate_limited", "too many requests; try again later")
		},
		ErrorHandler: func(*echo.Context, error) error {
			return newAPIError(http.StatusBadRequest, "invalid_request", "could not identify the client")
		},
	})
}

func ipLimiter(perMinute int) echo.MiddlewareFunc {
	return limiter(perMinute, func(c *echo.Context) (string, error) { return c.RealIP(), nil })
}

func playerLimiter(perMinute int) echo.MiddlewareFunc {
	return limiter(perMinute, func(c *echo.Context) (string, error) { return playerID(c), nil })
}

// --- バリデーション ---

type structValidator struct{ v *validator.Validate }

func newValidator() *structValidator {
	v := validator.New(validator.WithRequiredStructEnabled())
	// エラーメッセージのフィールド名は、Go の名前ではなく JSON の名前にする
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		name := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		return name
	})
	return &structValidator{v: v}
}

// Validate は echo.Validator を満たす。
func (s *structValidator) Validate(i any) error { return s.v.Struct(i) }
