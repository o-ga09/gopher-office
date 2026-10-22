// Package api はゲーム API サーバ（認証・プロフィール・ワークスペース・入室）。
package api

import "time"

// Player はゲストのプレイヤー。
type Player struct {
	ID          string    `gorm:"primaryKey;size:26"`
	DisplayName string    `gorm:"size:128;not null"`
	Gender      uint8     `gorm:"not null"`
	IconHash    *string   `gorm:"size:64"`
	CreatedAt   time.Time `gorm:"precision:3;not null"`
}

// Workspace は招待 URL が指す単位。
type Workspace struct {
	ID         string    `gorm:"primaryKey;size:26"`
	OwnerID    string    `gorm:"size:26;not null"`
	Name       string    `gorm:"size:64;not null"`
	InviteCode string    `gorm:"size:16;not null"`
	MaxPlayers int       `gorm:"not null"`
	CreatedAt  time.Time `gorm:"precision:3;not null"`
}

// Room は 1 つの GameServer に対応するルーム。v0 は 1 ワークスペースに 1 つ。
type Room struct {
	ID          string `gorm:"primaryKey;size:26"`
	WorkspaceID string `gorm:"size:26;not null"`
	Name        string `gorm:"size:64;not null"`
	MapID       string `gorm:"size:32;not null"`
}
