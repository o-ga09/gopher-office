package api

import (
	"net/http"
	"testing"
)

const testPassword = "correct horse battery"

func (env *testEnv) upgrade(t *testing.T, token, email, pw string) response {
	t.Helper()
	return env.do(t, http.MethodPost, "/v1/me/credentials", token, map[string]any{"email": email, "password": pw})
}

func TestAccountUpgradeAndLogin(t *testing.T) {
	env := newTestEnv(t)
	id, tok := env.guest(t, "alice", 1)

	if m := env.do(t, http.MethodGet, "/v1/me", tok, nil).json(t); m["account"] != nil {
		t.Fatalf("guest account = %v", m["account"])
	}
	res := env.upgrade(t, tok, "  Alice@Example.COM ", testPassword)
	if res.Code != http.StatusOK {
		t.Fatalf("upgrade: %d %s", res.Code, res.Body)
	}
	if res.json(t)["player"].(map[string]any)["id"] != id {
		t.Fatal("player_id changed")
	}
	me := env.do(t, http.MethodGet, "/v1/me", tok, nil).json(t)
	if me["account"].(map[string]any)["email"] != "alice@example.com" {
		t.Fatalf("account = %v", me["account"])
	}

	res = env.do(t, http.MethodPost, "/v1/login", "", map[string]any{"email": "ALICE@example.com", "password": testPassword})
	if res.Code != http.StatusOK {
		t.Fatalf("login: %d %s", res.Code, res.Body)
	}
	m := res.json(t)
	if m["player"].(map[string]any)["id"] != id {
		t.Fatal("login returned another player")
	}
	if env.do(t, http.MethodGet, "/v1/me", m["player_token"].(string), nil).Code != http.StatusOK {
		t.Fatal("login token rejected")
	}
}

func TestAccountConflicts(t *testing.T) {
	env := newTestEnv(t)
	_, a := env.guest(t, "a", 0)
	_, b := env.guest(t, "b", 0)
	if r := env.upgrade(t, a, "x@example.com", testPassword); r.Code != http.StatusOK {
		t.Fatal(r.Body)
	}
	if r := env.upgrade(t, b, "X@example.com", testPassword); r.Code != http.StatusConflict || r.errorCode(t) != "email_taken" {
		t.Fatalf("dup email: %d %s", r.Code, r.Body)
	}
	if r := env.upgrade(t, a, "y@example.com", testPassword); r.Code != http.StatusConflict || r.errorCode(t) != "already_registered" {
		t.Fatalf("again: %d %s", r.Code, r.Body)
	}
	if r := env.upgrade(t, b, "not-an-email", testPassword); r.Code != http.StatusBadRequest {
		t.Fatalf("bad email: %d", r.Code)
	}
	if r := env.upgrade(t, b, "b@example.com", "short"); r.Code != http.StatusBadRequest {
		t.Fatalf("short pw: %d", r.Code)
	}
	if r := env.upgrade(t, "", "b@example.com", testPassword); r.Code != http.StatusUnauthorized {
		t.Fatalf("no auth: %d", r.Code)
	}
}

func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	env := newTestEnv(t)
	_, tok := env.guest(t, "a", 0)
	env.upgrade(t, tok, "a@example.com", testPassword)

	wrong := env.do(t, http.MethodPost, "/v1/login", "", map[string]any{"email": "a@example.com", "password": "wrong password"})
	missing := env.do(t, http.MethodPost, "/v1/login", "", map[string]any{"email": "nobody@example.com", "password": "wrong password"})
	if wrong.Code != http.StatusUnauthorized || missing.Code != http.StatusUnauthorized {
		t.Fatalf("codes: %d, %d", wrong.Code, missing.Code)
	}
	if wrong.Body.String() != missing.Body.String() || wrong.errorCode(t) != "invalid_credentials" {
		t.Fatalf("bodies differ: %s vs %s", wrong.Body, missing.Body)
	}
	bad := env.do(t, http.MethodPost, "/v1/login", "", `{"email":1}`)
	if bad.Code != http.StatusUnauthorized || bad.errorCode(t) != "invalid_credentials" {
		t.Fatalf("malformed: %d %s", bad.Code, bad.Body)
	}
}

func TestLoginRateLimit(t *testing.T) {
	env := newTestEnv(t)
	var last response
	for i := 0; i < loginPerMinute+1; i++ {
		last = env.do(t, http.MethodPost, "/v1/login", "", map[string]any{"email": "n@example.com", "password": "wrong password"})
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d", last.Code)
	}
}

func TestChangePasswordRevokesOldTokens(t *testing.T) {
	env := newTestEnv(t)
	id, old := env.guest(t, "a", 0)
	env.upgrade(t, old, "a@example.com", testPassword)

	body := map[string]any{"current_password": "nope nope nope", "new_password": "brand new password"} //nolint:gosec // テスト用ダミー
	if r := env.do(t, http.MethodPut, "/v1/me/password", old, body); r.Code != http.StatusUnauthorized || r.errorCode(t) != "invalid_credentials" {
		t.Fatalf("wrong current: %d %s", r.Code, r.Body)
	}
	if r := env.do(t, http.MethodPut, "/v1/me/password", old, map[string]any{"current_password": testPassword, "new_password": "short"}); r.Code != http.StatusBadRequest {
		t.Fatalf("short new: %d", r.Code)
	}
	body["current_password"] = testPassword
	res := env.do(t, http.MethodPut, "/v1/me/password", old, body)
	if res.Code != http.StatusOK {
		t.Fatalf("change: %d %s", res.Code, res.Body)
	}
	fresh := res.json(t)["player_token"].(string)

	if env.do(t, http.MethodGet, "/v1/me", old, nil).Code != http.StatusUnauthorized {
		t.Fatal("old token still valid")
	}
	if r := env.do(t, http.MethodGet, "/v1/me", fresh, nil); r.Code != http.StatusOK || r.json(t)["player"].(map[string]any)["id"] != id {
		t.Fatalf("new token: %d", r.Code)
	}
	if r := env.do(t, http.MethodPost, "/v1/login", "", map[string]any{"email": "a@example.com", "password": testPassword}); r.Code != http.StatusUnauthorized {
		t.Fatal("old password still works")
	}
	r := env.do(t, http.MethodPost, "/v1/login", "", map[string]any{"email": "a@example.com", "password": "brand new password"})
	if r.Code != http.StatusOK {
		t.Fatalf("new password login: %d", r.Code)
	}
	if env.do(t, http.MethodGet, "/v1/me", r.json(t)["player_token"].(string), nil).Code != http.StatusOK {
		t.Fatal("login token after change rejected")
	}
}

func TestChangePasswordGuest(t *testing.T) {
	env := newTestEnv(t)
	_, tok := env.guest(t, "a", 0)
	r := env.do(t, http.MethodPut, "/v1/me/password", tok, map[string]any{"current_password": testPassword, "new_password": "brand new password"})
	if r.Code != http.StatusConflict {
		t.Fatalf("guest change: %d", r.Code)
	}
}

func TestCredentialsEndpointsAreRateLimited(t *testing.T) {
	env := newTestEnv(t)
	_, tok := env.guest(t, "a", 0)

	var last response
	for i := 0; i < credentialsPerMinute+1; i++ {
		last = env.upgrade(t, tok, "not-an-email", testPassword)
	}
	if last.Code != http.StatusTooManyRequests || last.errorCode(t) != "rate_limited" {
		t.Fatalf("POST /me/credentials: %d %s", last.Code, last.Body)
	}

	_, tok2 := env.guest(t, "b", 0)
	body := map[string]any{"current_password": "wrong password", "new_password": "brand new password"}
	for i := 0; i < credentialsPerMinute+1; i++ {
		last = env.do(t, http.MethodPut, "/v1/me/password", tok2, body)
	}
	if last.Code != http.StatusTooManyRequests || last.errorCode(t) != "rate_limited" {
		t.Fatalf("PUT /me/password: %d %s", last.Code, last.Body)
	}
}
