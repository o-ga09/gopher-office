//go:build !js

package main

import (
	"flag"

	"github.com/o-ga09/gopher-office/internal/client"
)

// loadConfig はコマンドラインのフラグから設定を読む（デスクトップ版）。
// -server を指定しなければオフライン（自分だけ）で動く。
func loadConfig() client.Config {
	server := flag.String("server", "", "GameServer の WebSocket URL（例: ws://localhost:7654/room）")
	token := flag.String("token", "", "roomToken")
	name := flag.String("name", "", "表示名")
	flag.Parse()
	return client.Config{ServerURL: *server, RoomToken: *token, Name: *name}
}
