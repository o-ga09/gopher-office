package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v5"
	"github.com/oklog/ulid/v2"

	"github.com/o-ga09/gopher-office/internal/auth"
	"github.com/o-ga09/gopher-office/internal/profile"
)

// routesWorkspace はワークスペース（作成・招待・入室）のルートを登録する。
func (h *handler) routesWorkspace(v1 *echo.Group, limit echo.MiddlewareFunc) {
	v1.GET("/workspaces/by-code/:code", h.getWorkspaceByCode)
	v1.POST("/workspaces", h.createWorkspace, h.requireAuth, limit)
	v1.POST("/workspaces/:code/join", h.joinWorkspace, h.requireAuth, limit)
	v1.POST("/workspaces/:code/rooms", h.createRoom, h.requireAuth, limit)
	v1.GET("/me/workspaces", h.myWorkspaces, h.requireAuth)
}

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
	h.recordMember(ctx, w, owner.ID)
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
	ctx := c.Request().Context()
	w, _, err := h.cfg.Store.GetWorkspaceByCode(ctx, code)
	if errors.Is(err, ErrNotFound) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	rooms, err := h.cfg.Store.ListRooms(ctx, w.ID)
	if err != nil {
		return err
	}
	views := make([]roomView, len(rooms))
	for i, r := range rooms {
		views[i] = roomView{ID: r.ID, Name: r.Name}
	}
	return c.JSON(http.StatusOK, map[string]any{"workspace": map[string]any{"name": w.Name, "max_players": w.MaxPlayers, "rooms": views}})
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
	req, err := readJoinRequest(c)
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
	if req.RoomID != "" && req.RoomID != room.ID {
		// 最初のルーム以外が指定された。そのワークスペースのルームでなければ 404
		if room, err = h.cfg.Store.GetRoom(ctx, w.ID, req.RoomID); errors.Is(err, ErrNotFound) {
			return errNotFound
		} else if err != nil {
			return err
		}
	}
	h.recordMember(ctx, w, player.ID)

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

type roomView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type joinRequest struct {
	RoomID string `json:"room_id" validate:"max=26"`
}

// readJoinRequest は入室のボディを読む。ボディは任意で、空なら最初のルームに入る（後方互換）。
func readJoinRequest(c *echo.Context) (joinRequest, error) {
	var req joinRequest
	raw, err := io.ReadAll(c.Request().Body)
	if err != nil {
		var sc echo.HTTPStatusCoder
		if errors.As(err, &sc) && sc.StatusCode() == http.StatusRequestEntityTooLarge {
			return req, err
		}
		return req, badRequest("could not read the request body")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return req, nil
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return req, badRequest("the request body is not valid JSON")
	}
	return req, c.Validate(&req)
}

// recordMember は参加者を記録する。一覧のための付加情報なので、失敗しても入室は止めずログだけ残す。
func (h *handler) recordMember(ctx context.Context, w *Workspace, playerID string) {
	role := roleMember
	if w.OwnerID == playerID {
		role = roleOwner
	}
	now := time.Now().UTC()
	if err := h.cfg.Store.UpsertMember(ctx, &WorkspaceMember{
		WorkspaceID: w.ID, PlayerID: playerID, Role: role, FirstJoinedAt: now, LastJoinedAt: now,
	}); err != nil {
		h.log.Error("record workspace member failed", "workspace", w.ID, "err", err)
	}
}

type createRoomRequest struct {
	Name string `json:"name" validate:"required,max=256"`
}

// createRoom はワークスペースにルームを足す（オーナーのみ）。
func (h *handler) createRoom(c *echo.Context) error {
	code, err := inviteCode(c)
	if err != nil {
		return err
	}
	var req createRoomRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	name := profile.CleanText(req.Name, maxWorkspaceNameRunes)
	if name == "" {
		return badRequest("name must contain visible characters")
	}
	ctx := c.Request().Context()
	w, _, err := h.cfg.Store.GetWorkspaceByCode(ctx, code)
	if errors.Is(err, ErrNotFound) {
		return errNotFound
	}
	if err != nil {
		return err
	}
	if w.OwnerID != playerID(c) {
		return errForbidden
	}
	n, err := h.cfg.Store.CountRooms(ctx, w.ID)
	if err != nil {
		return err
	}
	if n >= int64(h.cfg.MaxRoomsPerWorkspace) {
		return newAPIError(http.StatusConflict, "room_limit", "this workspace has reached the maximum number of rooms")
	}
	r := &Room{ID: ulid.Make().String(), WorkspaceID: w.ID, Name: name, MapID: defaultMapID}
	if err := h.cfg.Store.CreateRoom(ctx, r); err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, map[string]any{"room": roomView{ID: r.ID, Name: r.Name}})
}

type myWorkspaceView struct {
	workspaceView
	Role         string `json:"role"`
	LastJoinedAt string `json:"last_joined_at"`
}

// myWorkspaces は自分がオーナーまたは参加済みのワークスペースを、新しく入った順に返す。
func (h *handler) myWorkspaces(c *echo.Context) error {
	list, err := h.cfg.Store.ListMyWorkspaces(c.Request().Context(), playerID(c), maxMyWorkspaces)
	if err != nil {
		return err
	}
	views := make([]myWorkspaceView, len(list))
	for i, m := range list {
		role := "member"
		if m.Role == roleOwner {
			role = "owner"
		}
		views[i] = myWorkspaceView{workspaceView: workspaceViewOf(&m.Workspace), Role: role, LastJoinedAt: m.LastJoinedAt.UTC().Format(time.RFC3339)}
	}
	return c.JSON(http.StatusOK, map[string]any{"workspaces": views})
}
