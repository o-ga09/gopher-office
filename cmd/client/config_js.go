//go:build js

package main

import (
	"net/url"
	"syscall/js"

	"github.com/o-ga09/gopher-office/internal/client"
)

// loadConfig はページの URL のクエリから設定を読む（ブラウザ版）。
//
//	?online=1                   同じオリジンの /room に接続する
//	?server=ws://host:7654/room 接続先を指定する（指定すると online=1 は不要）
//	?name=たいち                 表示名
//	?token=...                  roomToken
//
// どれも無ければオフライン（自分だけ）で動く。
func loadConfig() client.Config {
	loc := js.Global().Get("location")
	u, err := url.Parse(loc.Get("href").String())
	if err != nil {
		return client.Config{}
	}
	q := u.Query()
	cfg := client.Config{ServerURL: q.Get("server"), RoomToken: q.Get("token"), Name: q.Get("name")}
	if cfg.ServerURL == "" && q.Get("online") == "1" {
		scheme := "ws"
		if u.Scheme == "https" {
			scheme = "wss"
		}
		cfg.ServerURL = scheme + "://" + u.Host + "/room"
	}
	return cfg
}
