package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
)

const (
	ctxPlayerID = "player_id"

	maxWorkspaceNameRunes = 64
	defaultMaxPlayers     = 200
	defaultMapID          = "office-1"
	inviteCodeRetries     = 5

	iconUploadPath = "/v1/me/icon"
)

// --- 認証 ---

func playerID(c *echo.Context) string {
	id, _ := c.Get(ctxPlayerID).(string)
	return id
}

// requireAuth は Authorization: Bearer <playerToken> を検証し、プレイヤー ID をコンテキストに載せる。
func (h *handler) requireAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		tok, ok := strings.CutPrefix(c.Request().Header.Get("Authorization"), "Bearer ")
		if !ok || tok == "" {
			return errUnauthorized
		}
		claims, err := h.cfg.Signer.ParsePlayerTokenClaims(tok)
		if err != nil {
			return errUnauthorized
		}
		// token_version を DB の値と照合する（パスワード変更で上がり、古いトークンを失効させる）
		p, err := h.cfg.Store.GetPlayer(c.Request().Context(), claims.PlayerID)
		if errors.Is(err, ErrNotFound) || (err == nil && p.TokenVersion != claims.Version) {
			return errUnauthorized
		}
		if err != nil {
			return err
		}
		c.Set(ctxPlayerID, claims.PlayerID)
		return next(c)
	}
}

// --- プレイヤー ---

// --- ワークスペース ---

// bindAndValidate はリクエストの JSON を構造体に読み込み、validator で検証する。
func bindAndValidate(c *echo.Context, dst any) error {
	if err := c.Bind(dst); err != nil {
		var sc echo.HTTPStatusCoder
		if errors.As(err, &sc) && sc.StatusCode() == http.StatusRequestEntityTooLarge {
			return err
		}
		return badRequest("the request body is not valid JSON")
	}
	return c.Validate(dst)
}
