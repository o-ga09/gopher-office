package apiclient

import (
	"context"
	"net/http"
)

// Account は永続アカウント（メール + パスワード）の情報。ゲストなら Player.Account は nil。
type Account struct {
	Email string `json:"email"`
}

type accountResult struct {
	Player Player `json:"player"`
	Token  string `json:"player_token"`
}

func (c *Client) applyAccount(out accountResult, email string) Player {
	c.token = out.Token
	if email != "" {
		out.Player.Account = &Account{Email: email}
	}
	return out.Player
}

// RegisterCredentials はゲストにメールとパスワードを設定する（player_id は変わらない）。新しい playerToken を設定する。
func (c *Client) RegisterCredentials(ctx context.Context, email, password string) (Player, error) {
	var out accountResult
	if err := c.do(ctx, http.MethodPost, "/v1/me/credentials", map[string]any{"email": email, "password": password}, &out); err != nil {
		return Player{}, err
	}
	return c.applyAccount(out, email), nil
}

// Login はメールとパスワードでログインし、playerToken を設定する。
func (c *Client) Login(ctx context.Context, email, password string) (Player, error) {
	var out struct {
		accountResult
		Account *Account `json:"account"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/login", map[string]any{"email": email, "password": password}, &out); err != nil {
		return Player{}, err
	}
	c.token = out.Token
	out.Player.Account = out.Account
	return out.Player, nil
}

// ChangePassword はパスワードを変え、新しい playerToken を設定する（古いトークンは失効する）。
func (c *Client) ChangePassword(ctx context.Context, current, next string) error {
	var out accountResult
	if err := c.do(ctx, http.MethodPut, "/v1/me/password", map[string]any{"current_password": current, "new_password": next}, &out); err != nil {
		return err
	}
	c.token = out.Token
	return nil
}
