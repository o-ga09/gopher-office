// Package apiclient はゲーム API の Go クライアント。ブラウザ（WASM）でもネイティブ（bot）でも動く。
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxResponseBytes はレスポンスのボディの上限。API のレスポンスは数百バイトしかない。
const maxResponseBytes = 1 << 20

// Error は API が返したエラー。
type Error struct {
	Status  int
	Code    string // not_found, unauthorized, rate_limited, unavailable, invalid_request など
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("api error %d %s: %s", e.Status, e.Code, e.Message)
}

// IsCode は、err が指定したコードの API エラーかを返す。
func IsCode(err error, code string) bool {
	var ae *Error
	return errors.As(err, &ae) && ae.Code == code
}

// Player は API のプレイヤー。
type Player struct {
	ID          string  `json:"id"`
	DisplayName string  `json:"display_name"`
	Gender      uint8   `json:"gender"`
	IconHash    *string `json:"icon_hash"`
}

// JoinResult は入室の結果。
type JoinResult struct {
	ServerURL string    `json:"server_url"`
	RoomToken string    `json:"room_token"`
	ExpiresAt time.Time `json:"expires_at"`
	Room      struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		MapID string `json:"map_id"`
	} `json:"room"`
	Workspace struct {
		Name string `json:"name"`
	} `json:"workspace"`
}

// Workspace はワークスペース。
type Workspace struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	InviteCode string `json:"invite_code"`
	InvitePath string `json:"invite_path"`
	MaxPlayers int    `json:"max_players"`
}

// Client は API クライアント。
type Client struct {
	base  string
	http  *http.Client
	token string
}

// New は API のベース URL（例: http://localhost:8080）からクライアントを作る。
func New(baseURL string) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: 15 * time.Second}}
}

// SetToken は認証に使う playerToken を設定する。
func (c *Client) SetToken(token string) { c.token = token }

// Token は設定されている playerToken を返す。
func (c *Client) Token() string { return c.token }

// CreateGuest はゲストを作り、playerToken をクライアントに設定する。
func (c *Client) CreateGuest(ctx context.Context, displayName string, gender uint8) (Player, error) {
	var out struct {
		Player Player `json:"player"`
		Token  string `json:"player_token"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/guest", map[string]any{"display_name": displayName, "gender": gender}, &out); err != nil {
		return Player{}, err
	}
	c.token = out.Token
	return out.Player, nil
}

// Me は自分のプロフィールを返す。
func (c *Client) Me(ctx context.Context) (Player, error) {
	var out struct {
		Player Player `json:"player"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/me", nil, &out)
	return out.Player, err
}

// UpdateMe はプロフィールを更新する。
func (c *Client) UpdateMe(ctx context.Context, displayName string, gender uint8) (Player, error) {
	var out struct {
		Player Player `json:"player"`
	}
	err := c.do(ctx, http.MethodPatch, "/v1/me", map[string]any{"display_name": displayName, "gender": gender}, &out)
	return out.Player, err
}

// CreateWorkspace はワークスペースを作る。
func (c *Client) CreateWorkspace(ctx context.Context, name string) (Workspace, error) {
	var out struct {
		Workspace Workspace `json:"workspace"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/workspaces", map[string]any{"name": name}, &out)
	return out.Workspace, err
}

// WorkspaceName は招待コードのワークスペース名を返す（認証不要）。
func (c *Client) WorkspaceName(ctx context.Context, code string) (string, error) {
	var out struct {
		Workspace struct {
			Name string `json:"name"`
		} `json:"workspace"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/workspaces/by-code/"+url.PathEscape(code), nil, &out)
	return out.Workspace.Name, err
}

// Join は招待コードのワークスペースに入るための roomToken と接続先を受け取る。
func (c *Client) Join(ctx context.Context, code string) (JoinResult, error) {
	var out JoinResult
	err := c.do(ctx, http.MethodPost, "/v1/workspaces/"+url.PathEscape(code)+"/join", nil, &out)
	return out, err
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return err
	}

	if resp.StatusCode >= 400 {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &e) // JSON でないエラー（プロキシの 502 など）でも、ステータスは返す
		return &Error{Status: resp.StatusCode, Code: e.Error.Code, Message: e.Error.Message}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}
