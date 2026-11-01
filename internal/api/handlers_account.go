package api

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/o-ga09/gopher-office/internal/password"
)

const (
	loginPerMinute = 10
	// 昇格とパスワード変更は、パスワードの総当たりや argon2 の負荷かけに使われうるので、厳しめに絞る
	credentialsPerMinute = 5
	maxEmailLen          = 254
	argonConcurrent      = 4
)

// argon2id は 1 回で約 19 MiB を使う。同時に走らせる数を絞って、メモリを使い切られないようにする。
var argonSem = make(chan struct{}, argonConcurrent)

func withArgon[T any](ctx context.Context, f func() (T, error)) (T, error) {
	select {
	case argonSem <- struct{}{}:
		defer func() { <-argonSem }()
		return f()
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

var (
	errInvalidCredentials = newAPIError(http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect")
	errEmailTaken         = newAPIError(http.StatusConflict, "email_taken", "this email is already registered")
	errAlreadyRegistered  = newAPIError(http.StatusConflict, "already_registered", "this account already has credentials")
)

// routesAccount はアカウント（昇格・ログイン・パスワード変更）のルートを登録する。Accounts が無ければ何もしない。
func (h *handler) routesAccount(v1 *echo.Group) {
	if h.cfg.Accounts == nil {
		return
	}
	// プレイヤー単位と IP 単位の両方で絞る（requireAuth の後ろに置いて、プレイヤー ID を使えるようにする）
	byPlayer, byIP := playerLimiter(credentialsPerMinute), ipLimiter(credentialsPerMinute)
	v1.POST("/me/credentials", h.registerCredentials, h.requireAuth, byPlayer, byIP)
	v1.PUT("/me/password", h.changePassword, h.requireAuth, byPlayer, byIP)
	v1.POST("/login", h.login, ipLimiter(loginPerMinute))
}

// normalizeEmail はメールを trim + 小文字化し、形式と長さを検査する。
func normalizeEmail(s string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(s))
	if e == "" || len(e) > maxEmailLen {
		return "", badRequest("email is invalid")
	}
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e || a.Name != "" {
		return "", badRequest("email is invalid")
	}
	return e, nil
}

func passwordError(err error) error {
	if errors.Is(err, password.ErrPasswordLength) {
		return badRequest(err.Error())
	}
	return err
}

func (h *handler) tokenResponse(c *echo.Context, status int, p *Player, ver uint32, email string) error {
	token, err := h.cfg.Signer.IssuePlayerTokenV(p.ID, ver)
	if err != nil {
		return err
	}
	out := map[string]any{"player": viewOf(p), "player_token": token}
	if email != "" {
		out["account"] = map[string]string{"email": email}
	}
	return c.JSON(status, out)
}

type credentialsRequest struct {
	Email    string `json:"email" validate:"required,max=512"`
	Password string `json:"password" validate:"required,max=1024"`
}

// registerCredentials はゲストにメールとパスワードを設定する（昇格）。player_id は変わらない。
func (h *handler) registerCredentials(c *echo.Context) error {
	var req credentialsRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	email, err := normalizeEmail(req.Email)
	if err != nil {
		return err
	}
	if err := password.ValidatePassword(req.Password); err != nil {
		return passwordError(err)
	}
	ctx := c.Request().Context()
	p, err := h.currentPlayer(c)
	if err != nil {
		return err
	}
	if _, err := h.cfg.Accounts.GetCredential(ctx, p.ID); err == nil {
		return errAlreadyRegistered
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	hash, err := withArgon(ctx, func() (string, error) { return password.HashPassword(req.Password) })
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	err = h.cfg.Accounts.CreateCredential(ctx, &Credential{PlayerID: p.ID, Email: email, PasswordHash: hash, CreatedAt: now, UpdatedAt: now})
	if errors.Is(err, ErrDuplicate) {
		return errEmailTaken
	}
	if err != nil {
		return err
	}
	return h.tokenResponse(c, http.StatusOK, p, p.TokenVersion, email)
}

type loginRequest struct {
	Email    string `json:"email" validate:"required,max=512"`
	Password string `json:"password" validate:"required,max=1024"`
}

// login はメールとパスワードでログインする。失敗の理由は区別せず、すべて 401 invalid_credentials にする。
func (h *handler) login(c *echo.Context) error {
	var req loginRequest
	if err := bindAndValidate(c, &req); err != nil {
		return errInvalidCredentials
	}
	ctx := c.Request().Context()
	email := strings.ToLower(strings.TrimSpace(req.Email))

	cred, err := h.cfg.Accounts.GetCredentialByEmail(ctx, email)
	hash := ""
	switch {
	case err == nil:
		hash = cred.PasswordHash
	case errors.Is(err, ErrNotFound):
		// メールの存在を、応答時間で漏らさないために、ダミーのハッシュを計算する
		hash = h.dummyHash
	default:
		return err
	}
	ok, verr := withArgon(ctx, func() (bool, error) { return password.VerifyPassword(req.Password, hash) })
	if verr != nil && !errors.Is(verr, password.ErrInvalidHash) {
		return verr
	}
	if cred == nil || verr != nil || !ok {
		return errInvalidCredentials
	}
	p, err := h.cfg.Store.GetPlayer(ctx, cred.PlayerID)
	if errors.Is(err, ErrNotFound) {
		return errInvalidCredentials
	}
	if err != nil {
		return err
	}
	return h.tokenResponse(c, http.StatusOK, p, p.TokenVersion, cred.Email)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password" validate:"required,max=1024"`
	NewPassword     string `json:"new_password" validate:"required,max=1024"`
}

// changePassword はパスワードを変える。token_version が上がり、古いトークンはすべて失効する。新しいトークンを返す。
func (h *handler) changePassword(c *echo.Context) error {
	var req changePasswordRequest
	if err := bindAndValidate(c, &req); err != nil {
		return err
	}
	if err := password.ValidatePassword(req.NewPassword); err != nil {
		return passwordError(err)
	}
	ctx := c.Request().Context()
	p, err := h.currentPlayer(c)
	if err != nil {
		return err
	}
	cred, err := h.cfg.Accounts.GetCredential(ctx, p.ID)
	if errors.Is(err, ErrNotFound) {
		return newAPIError(http.StatusConflict, "no_credentials", "this account has no credentials yet")
	}
	if err != nil {
		return err
	}
	ok, err := withArgon(ctx, func() (bool, error) { return password.VerifyPassword(req.CurrentPassword, cred.PasswordHash) })
	if err != nil && !errors.Is(err, password.ErrInvalidHash) {
		return err
	}
	if err != nil || !ok {
		return errInvalidCredentials
	}
	newHash, err := withArgon(ctx, func() (string, error) { return password.HashPassword(req.NewPassword) })
	if err != nil {
		return err
	}
	ver, err := h.cfg.Accounts.ChangePassword(ctx, p.ID, newHash)
	if errors.Is(err, ErrNotFound) {
		return errUnauthorized
	}
	if err != nil {
		return err
	}
	return h.tokenResponse(c, http.StatusOK, p, ver, cred.Email)
}

// accountOf は GET /v1/me の account（登録済みなら {email}、ゲストなら nil）。
func (h *handler) accountOf(ctx context.Context, playerID string) (any, error) {
	if h.cfg.Accounts == nil {
		return nil, nil
	}
	cred, err := h.cfg.Accounts.GetCredential(ctx, playerID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]string{"email": cred.Email}, nil
}
