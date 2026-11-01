package apiclient

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Room はワークスペースのルーム。
type Room struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MyWorkspace は自分が参加しているワークスペース。
type MyWorkspace struct {
	Workspace
	Role         string    `json:"role"` // "owner" または "member"
	LastJoinedAt time.Time `json:"last_joined_at"`
}

// JoinRoom は招待コードのワークスペースの、指定したルームに入る。roomID が空なら最初のルーム。
func (c *Client) JoinRoom(ctx context.Context, code, roomID string) (JoinResult, error) {
	var body any
	if roomID != "" {
		body = map[string]any{"room_id": roomID}
	}
	var out JoinResult
	err := c.do(ctx, http.MethodPost, "/v1/workspaces/"+url.PathEscape(code)+"/join", body, &out)
	return out, err
}

// Rooms は招待コードのワークスペースのルームを返す（認証不要）。
func (c *Client) Rooms(ctx context.Context, code string) ([]Room, error) {
	var out struct {
		Workspace struct {
			Rooms []Room `json:"rooms"`
		} `json:"workspace"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/workspaces/by-code/"+url.PathEscape(code), nil, &out)
	return out.Workspace.Rooms, err
}

// CreateRoom はワークスペースにルームを作る（オーナーのみ）。
func (c *Client) CreateRoom(ctx context.Context, code, name string) (Room, error) {
	var out struct {
		Room Room `json:"room"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/workspaces/"+url.PathEscape(code)+"/rooms", map[string]any{"name": name}, &out)
	return out.Room, err
}

// MyWorkspaces は自分がオーナーまたは参加済みのワークスペースを、新しく入った順に返す。
func (c *Client) MyWorkspaces(ctx context.Context) ([]MyWorkspace, error) {
	var out struct {
		Workspaces []MyWorkspace `json:"workspaces"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/me/workspaces", nil, &out)
	return out.Workspaces, err
}
