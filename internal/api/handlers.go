package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v5"
	"github.com/oklog/ulid/v2"

	"github.com/o-ga09/gopher-office/internal/auth"
	"github.com/o-ga09/gopher-office/internal/profile"
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
		id, err := h.cfg.Signer.ParsePlayerToken(tok)
		if err != nil {
			return errUnauthorized
		}
		c.Set(ctxPlayerID, id)
		return next(c)
	}
}

// --- プレイヤー ---

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
	token, err := h.cfg.Signer.IssuePlayerToken(p.ID)
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
	return c.JSON(http.StatusOK, map[string]any{"player": viewOf(p)})
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

// --- ワークスペース ---

type workspaceView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	InviteCode string `json:"invite_code"`
	// InvitePath は招待 URL のパス部分。ホストは呼び出し側（クライアントが開いているオリジン）が補う
	InvitePath string `json:"invite_path"`
	MaxPlayers int    `json:"max_players"`
}

func workspaceViewOf(w *Workspace) workspaceView {
	return workspaceView{ID: w.ID, Name: w.Name, InviteCode: w.InviteCode, InvitePath: "/w/" + w.InviteCode, MaxPlayers: w.MaxPlayers}
}

type createWorkspaceRequest struct {
	Name string `json:"name" validate:"required,max=256"`
}

func (h *handler) createWorkspace(c *echo.Context) error {
	var req createWorkspaceRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	name := profile.CleanText(req.Name, maxWorkspaceNameRunes)
	if name == "" {
		return badRequest("name must contain visible characters")
	}

	ctx := c.Request().Context()
	owner, err := h.currentPlayer(c)
	if err != nil {
		return err
	}
	n, err := h.cfg.Store.CountWorkspacesByOwner(ctx, owner.ID)
	if err != nil {
		return err
	}
	if n >= int64(h.cfg.MaxWorkspacesPerPlayer) {
		return newAPIError(http.StatusConflict, "workspace_limit", "you have reached the maximum number of workspaces")
	}

	w := &Workspace{ID: ulid.Make().String(), OwnerID: owner.ID, Name: name, MaxPlayers: defaultMaxPlayers, CreatedAt: time.Now().UTC()}
	r := &Room{ID: ulid.Make().String(), WorkspaceID: w.ID, Name: name, MapID: defaultMapID}
	for range inviteCodeRetries {
		if w.InviteCode, err = NewInviteCode(); err != nil {
			return err
		}
		err = h.cfg.Store.CreateWorkspace(ctx, w, r)
		if !errors.Is(err, ErrDuplicate) {
			break // 成功、または招待コードの衝突以外のエラー
		}
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, map[string]any{"workspace": workspaceViewOf(w)})
}

// inviteCode はパスの招待コードを検証して返す。形式が違うものは、存在しないのと同じ扱いにする
// （コードの形式から、何かを推測させない）。
func inviteCode(c *echo.Context) (string, error) {
	code := c.Param("code")
	if err := validator.New().Var(code, "required,len=12,alphanum"); err != nil {
		return "", errNotFound
	}
	return code, nil
}

// getWorkspaceByCode は招待ページ用に、ワークスペースの名前だけを返す（認証不要）。
func (h *handler) getWorkspaceByCode(c *echo.Context) error {
	code, err := inviteCode(c)
	if err != nil {
		return err
	}
	w, _, err := h.cfg.Store.GetWorkspaceByCode(c.Request().Context(), code)
	if errors.Is(err, ErrNotFound) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"workspace": map[string]any{"name": w.Name, "max_players": w.MaxPlayers}})
}

// joinWorkspace は招待コードのワークスペースに入るための roomToken を発行する。
// 満員かどうかの判定は GameServer が行う（入室時に Error{FULL} が返る）。
func (h *handler) joinWorkspace(c *echo.Context) error {
	code, err := inviteCode(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	player, err := h.currentPlayer(c)
	if err != nil {
		return err
	}
	w, room, err := h.cfg.Store.GetWorkspaceByCode(ctx, code)
	if errors.Is(err, ErrNotFound) {
		return errNotFound
	}
	if err != nil {
		return err
	}

	alloc, err := h.cfg.Allocator.Allocate(ctx, room)
	if err != nil {
		h.log.Error("allocate failed", "room", room.ID, "err", err)
		return newAPIError(http.StatusServiceUnavailable, "unavailable", "no room is available right now; try again later")
	}
	token, expiresAt, err := h.cfg.Signer.IssueRoomToken(auth.RoomClaims{
		PlayerID: player.ID, RoomID: room.ID, GameServer: alloc.GameServer,
	})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{
		"server_url": alloc.URL,
		"room_token": token,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
		"room":       map[string]any{"id": room.ID, "name": room.Name, "map_id": room.MapID},
		"workspace":  map[string]any{"name": w.Name},
	})
}

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
