package api

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RoomStore は複数ルームとワークスペースの参加者の永続化。
type RoomStore interface {
	// ListRooms はワークスペースのルームを ID 順（作成順）に返す。
	ListRooms(ctx context.Context, workspaceID string) ([]Room, error)
	// GetRoom はワークスペースのルームを返す。そのワークスペースに無ければ ErrNotFound。
	GetRoom(ctx context.Context, workspaceID, roomID string) (*Room, error)
	CountRooms(ctx context.Context, workspaceID string) (int64, error)
	CreateRoom(ctx context.Context, r *Room) error
	// UpsertMember は参加者を記録する。すでにいれば last_joined_at だけを更新する（役割は変えない）。
	UpsertMember(ctx context.Context, m *WorkspaceMember) error
	// ListMyWorkspaces はプレイヤーが参加しているワークスペースを、last_joined_at の降順で最大 limit 件返す。
	ListMyWorkspaces(ctx context.Context, playerID string, limit int) ([]MyWorkspace, error)
}

func (s *GormStore) ListRooms(ctx context.Context, workspaceID string) ([]Room, error) {
	var rs []Room
	err := s.db.WithContext(ctx).Where("workspace_id = ?", workspaceID).Order("id").Find(&rs).Error
	return rs, err
}

func (s *GormStore) GetRoom(ctx context.Context, workspaceID, roomID string) (*Room, error) {
	var r Room
	if err := s.db.WithContext(ctx).First(&r, "id = ? AND workspace_id = ?", roomID, workspaceID).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return &r, nil
}

func (s *GormStore) CountRooms(ctx context.Context, workspaceID string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&Room{}).Where("workspace_id = ?", workspaceID).Count(&n).Error
	return n, err
}

func (s *GormStore) CreateRoom(ctx context.Context, r *Room) error {
	return s.db.WithContext(ctx).Create(r).Error
}

func (s *GormStore) UpsertMember(ctx context.Context, m *WorkspaceMember) error {
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "workspace_id"}, {Name: "player_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"last_joined_at"}),
	}).Create(m).Error
}

func (s *GormStore) ListMyWorkspaces(ctx context.Context, playerID string, limit int) ([]MyWorkspace, error) {
	type row struct {
		Workspace
		MemberRole   uint8
		LastJoinedAt time.Time
	}
	var rows []row
	err := s.db.WithContext(ctx).Session(&gorm.Session{}).
		Table("workspace_members AS m").
		Select("w.*, m.role AS member_role, m.last_joined_at AS last_joined_at").
		Joins("JOIN workspaces w ON w.id = m.workspace_id").
		Where("m.player_id = ?", playerID).
		Order("m.last_joined_at DESC").Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]MyWorkspace, len(rows))
	for i, r := range rows {
		out[i] = MyWorkspace{Workspace: r.Workspace, Role: r.MemberRole, LastJoinedAt: r.LastJoinedAt}
	}
	return out, nil
}
