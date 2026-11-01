package api

import (
	"context"
	"errors"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

var (
	// ErrNotFound は対象が存在しないことを表す。
	ErrNotFound = errors.New("api: not found")
	// ErrDuplicate は一意制約（招待コードなど）に違反したことを表す。
	ErrDuplicate = errors.New("api: duplicate")
)

// PlayerUpdate は変更するフィールド。nil のフィールドは変更しない。
type PlayerUpdate struct {
	DisplayName *string
	Gender      *uint8
}

// Store は永続化の境界。ハンドラのテストでは、メモリ上の実装に差し替える。
type Store interface {
	CreatePlayer(ctx context.Context, p *Player) error
	GetPlayer(ctx context.Context, id string) (*Player, error)
	UpdatePlayer(ctx context.Context, id string, u PlayerUpdate) (*Player, error)

	CountWorkspacesByOwner(ctx context.Context, ownerID string) (int64, error)
	// CreateWorkspace はワークスペースとルームを 1 つのトランザクションで作る。招待コードが重複したら ErrDuplicate。
	CreateWorkspace(ctx context.Context, w *Workspace, r *Room) error
	// GetWorkspaceByCode は招待コードからワークスペースとそのルームを返す。無ければ ErrNotFound。
	GetWorkspaceByCode(ctx context.Context, code string) (*Workspace, *Room, error)

	Ping(ctx context.Context) error
}

// GormStore は GORM（MySQL）による Store。
type GormStore struct{ db *gorm.DB }

// NewGormStore は GORM の DB から Store を作る。
func NewGormStore(db *gorm.DB) *GormStore { return &GormStore{db: db} }

const mysqlDuplicateEntry = 1062

func isDuplicate(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == mysqlDuplicateEntry
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

func (s *GormStore) Ping(ctx context.Context) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

func mapNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}
