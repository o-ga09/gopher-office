//go:build js

package main

import (
	"net/url"
	"strings"
	"syscall/js"

	"github.com/o-ga09/gopher-office/internal/client"
)

// tokenKey は playerToken を保存する localStorage のキー。
const tokenKey = "gopher-office.player-token"

// loadConfig はページの URL から設定を読む（ブラウザ版）。
//
//	/w/{招待コード}              招待 URL。API でゲスト登録・入室し、返された GameServer につなぐ
//	?api=http://localhost:8080  API のベース URL（既定は同じオリジン）
//	?online=1                   API を通さず、同じオリジンの /room に直接つなぐ（開発用）
//	?server=ws://host:7654/room 接続先を直接指定する（開発用）
//	?name=たいち                 表示名
//
// どれも無ければオフライン（自分だけ）で動く。
func loadConfig() client.Config {
	u, err := url.Parse(js.Global().Get("location").Get("href").String())
	if err != nil {
		return client.Config{}
	}
	q := u.Query()
	origin := u.Scheme + "://" + u.Host
	cfg := client.Config{ServerURL: q.Get("server"), Name: q.Get("name")}

	if code, ok := strings.CutPrefix(u.Path, "/w/"); ok && code != "" {
		cfg.InviteCode = strings.Trim(code, "/")
		cfg.APIBase = origin
		if api := q.Get("api"); api != "" {
			cfg.APIBase = api
		}
		cfg.Store = localStorageTokens{}
		return cfg
	}

	if cfg.ServerURL == "" && q.Get("online") == "1" {
		scheme := "ws"
		if u.Scheme == "https" {
			scheme = "wss"
		}
		cfg.ServerURL = scheme + "://" + u.Host + "/room"
	}
	return cfg
}

// localStorageTokens は playerToken をブラウザの localStorage に保存する。
// プライベートブラウズなどで localStorage が使えなくても動くよう、例外は握りつぶす（保存されないだけ）。
type localStorageTokens struct{}

func (localStorageTokens) Load() (token string) {
	defer func() { _ = recover() }()
	v := js.Global().Get("localStorage").Call("getItem", tokenKey)
	if v.IsNull() || v.IsUndefined() {
		return ""
	}
	return v.String()
}

func (localStorageTokens) Save(token string) {
	defer func() { _ = recover() }()
	js.Global().Get("localStorage").Call("setItem", tokenKey, token)
}
