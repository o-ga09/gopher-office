package api

import (
	"context"

	"gorm.io/gorm"
)

// WorkspaceStore はワークスペースとルームの永続化。
type WorkspaceStore interface {
	CountWorkspacesByOwner(ctx context.Context, ownerID string) (int64, error)
	// CreateWorkspace はワークスペースとルームを 1 つのトランザクションで作る。招待コードが重複したら ErrDuplicate。
	CreateWorkspace(ctx context.Context, w *Workspace, r *Room) error
	// GetWorkspaceByCode は招待コードからワークスペースとそのルームを返す。無ければ ErrNotFound。
	GetWorkspaceByCode(ctx context.Context, code string) (*Workspace, *Room, error)
}

func (s *GormStore) CountWorkspacesByOwner(ctx context.Context, ownerID string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&Workspace{}).Where("owner_id = ?", ownerID).Count(&n).Error
	return n, err
}

func (s *GormStore) CreateWorkspace(ctx context.Context, w *Workspace, r *Room) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(w).Error; err != nil {
			return err
		}
		return tx.Create(r).Error
	})
	if isDuplicate(err) {
		return ErrDuplicate
	}
	return err
}

func (s *GormStore) GetWorkspaceByCode(ctx context.Context, code string) (*Workspace, *Room, error) {
	var w Workspace
	if err := s.db.WithContext(ctx).First(&w, "invite_code = ?", code).Error; err != nil {
		return nil, nil, mapNotFound(err)
	}
	var r Room
	if err := s.db.WithContext(ctx).Order("id").First(&r, "workspace_id = ?", w.ID).Error; err != nil {
		return nil, nil, mapNotFound(err)
	}
	return &w, &r, nil
}
