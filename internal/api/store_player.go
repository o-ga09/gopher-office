package api

import (
	"context"

	"gorm.io/gorm"
)

// PlayerUpdate は変更するフィールド。nil のフィールドは変更しない。
type PlayerUpdate struct {
	DisplayName *string
	Gender      *uint8
}

// PlayerStore はプレイヤーの永続化。
type PlayerStore interface {
	CreatePlayer(ctx context.Context, p *Player) error
	GetPlayer(ctx context.Context, id string) (*Player, error)
	UpdatePlayer(ctx context.Context, id string, u PlayerUpdate) (*Player, error)
}

func (s *GormStore) CreatePlayer(ctx context.Context, p *Player) error {
	return s.db.WithContext(ctx).Create(p).Error
}

func (s *GormStore) GetPlayer(ctx context.Context, id string) (*Player, error) {
	var p Player
	if err := s.db.WithContext(ctx).First(&p, "id = ?", id).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return &p, nil
}

func (s *GormStore) UpdatePlayer(ctx context.Context, id string, u PlayerUpdate) (*Player, error) {
	var p Player
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&p, "id = ?", id).Error; err != nil {
			return mapNotFound(err)
		}
		if u.DisplayName != nil {
			p.DisplayName = *u.DisplayName
		}
		if u.Gender != nil {
			p.Gender = *u.Gender
		}
		return tx.Save(&p).Error
	})
	if err != nil {
		return nil, err
	}
	return &p, nil
}
