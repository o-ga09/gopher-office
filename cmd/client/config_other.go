//go:build !js

package main

import (
	"flag"

	"github.com/o-ga09/gopher-office/internal/client"
)

// loadConfig はコマンドラインのフラグから設定を読む（デスクトップ版）。
// -server も -invite も指定しなければオフライン（自分だけ）で動く。
func loadConfig() client.Config {
	server := flag.String("server", "", "GameServer の WebSocket URL（API を通さず直接つなぐ。開発用）")
	token := flag.String("token", "", "roomToken（-server と組み合わせる）")
	name := flag.String("name", "", "表示名")
	invite := flag.String("invite", "", "招待コード。API でゲスト登録・入室する")
	api := flag.String("api", "http://localhost:8080", "API のベース URL（-invite と組み合わせる）")
	flag.Parse()

	cfg := client.Config{ServerURL: *server, RoomToken: *token, Name: *name}
	if *invite != "" {
		cfg.InviteCode, cfg.APIBase = *invite, *api
	}
	return cfg
}
