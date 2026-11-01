package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/oklog/ulid/v2"

	"github.com/o-ga09/gopher-office/internal/icon"
	"github.com/o-ga09/gopher-office/internal/profile"
)

// routesPlayer はプレイヤー（ゲスト・自分・アイコン）のルートを登録する。
func (h *handler) routesPlayer(v1 *echo.Group, guestLimit, limit echo.MiddlewareFunc) {
	v1.POST("/guest", h.createGuest, guestLimit)
	v1.GET("/players/:id/icon", h.getIcon)

	// 認証はルートごとに付ける。グループに付けると、存在しないパスにも認証が掛かり、404 が 401 になる
	v1.GET("/me", h.getMe, h.requireAuth)
	v1.PATCH("/me", h.updateMe, h.requireAuth)
	v1.PUT("/me/icon", h.putIcon, h.requireAuth, limit, middleware.BodyLimit(icon.MaxUploadBytes))
}

type playerView struct {
	ID          string  `json:"id"`
	DisplayName string  `json:"display_name"`
	Gender      uint8   `json:"gender"`
	IconHash    *string `json:"icon_hash"`
}

func viewOf(p *Player) playerView {
	return playerView{ID: p.ID, DisplayName: p.DisplayName, Gender: p.Gender, IconHash: p.IconHash}
}

type guestRequest struct {
	// 文字種は制限しない。長さの上限は、正規化（制御文字の除去・32 文字への切り詰め）の前の入力に対するもの
	DisplayName string `json:"display_name" validate:"max=256"`
	Gender      *uint8 `json:"gender" validate:"required,lte=2"`
}

func (h *handler) createGuest(c *echo.Context) error {
	var req guestRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	p := &Player{
		ID:          ulid.Make().String(),
		DisplayName: profile.CleanName(req.DisplayName),
		Gender:      *req.Gender,
		CreatedAt:   time.Now().UTC(),
	}
	if err := h.cfg.Store.CreatePlayer(c.Request().Context(), p); err != nil {
		return err
	}
	token, err := h.cfg.Signer.IssuePlayerTokenV(p.ID, p.TokenVersion)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, map[string]any{"player": viewOf(p), "player_token": token})
}

func (h *handler) getMe(c *echo.Context) error {
	p, err := h.currentPlayer(c)
	if err != nil {
		return err
	}
	account, err := h.accountOf(c.Request().Context(), p.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"player": viewOf(p), "account": account})
}

// currentPlayer はトークンのプレイヤーを DB から取る。削除済みなら、有効なトークンでも未認証として扱う。
func (h *handler) currentPlayer(c *echo.Context) (*Player, error) {
	p, err := h.cfg.Store.GetPlayer(c.Request().Context(), playerID(c))
	if errors.Is(err, ErrNotFound) {
		return nil, errUnauthorized
	}
	return p, err
}

type updateMeRequest struct {
	DisplayName *string `json:"display_name" validate:"omitempty,max=256"`
	Gender      *uint8  `json:"gender" validate:"omitempty,lte=2"`
}

func (h *handler) updateMe(c *echo.Context) error {
	var req updateMeRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	if req.DisplayName == nil && req.Gender == nil {
		return badRequest("specify display_name and/or gender")
	}
	var u PlayerUpdate
	if req.DisplayName != nil {
		name := profile.CleanName(*req.DisplayName)
		u.DisplayName = &name
	}
	u.Gender = req.Gender

	p, err := h.cfg.Store.UpdatePlayer(c.Request().Context(), playerID(c), u)
	if errors.Is(err, ErrNotFound) {
		return errUnauthorized
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"player": viewOf(p)})
}
