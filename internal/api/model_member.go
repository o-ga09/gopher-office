package api

import "time"

// ワークスペース内での役割。
const (
	roleMember uint8 = 0
	roleOwner  uint8 = 1
)

// WorkspaceMember はワークスペースの参加者（オーナーを含む）。
type WorkspaceMember struct {
	WorkspaceID   string    `gorm:"primaryKey;size:26"`
	PlayerID      string    `gorm:"primaryKey;size:26"`
	Role          uint8     `gorm:"not null"`
	FirstJoinedAt time.Time `gorm:"precision:3;not null"`
	LastJoinedAt  time.Time `gorm:"precision:3;not null"`
}

// MyWorkspace は自分が参加しているワークスペースと、その中での役割。
type MyWorkspace struct {
	Workspace    Workspace
	Role         uint8
	LastJoinedAt time.Time
}

// maxMyWorkspaces は /v1/me/workspaces が返す件数の上限。
const maxMyWorkspaces = 50
