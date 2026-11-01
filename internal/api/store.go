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

// Store は永続化の境界。ハンドラのテストでは、メモリ上の実装に差し替える。
type Store interface {
	PlayerStore
	IconStore
	WorkspaceStore
	RoomStore

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
