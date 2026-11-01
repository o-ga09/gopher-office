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
	// TokenVersion は playerToken の失効用。作成時にだけ書き込む（更新は ChangePassword だけが SQL で行う）
	TokenVersion uint32 `gorm:"<-:create;not null"`
}

// PlayerIcon はアイコン画像（再エンコード後の PNG）。Hash は Data の SHA-256 で、Player.IconHash が指す。
type PlayerIcon struct {
	Hash      string    `gorm:"primaryKey;size:64"`
	Data      []byte    `gorm:"type:mediumblob;not null"`
	CreatedAt time.Time `gorm:"precision:3;not null"`
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
