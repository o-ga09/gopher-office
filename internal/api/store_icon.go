package api

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// IconStore はアイコンの永続化。画像の本体は blob（オブジェクトストレージ）に置き、DB は icon_hash を持つ。
// 移行（docs/design.md §8 の段階 A〜C）の間は、DB の player_icons.data にも二重に書き、blob に無ければそこから読む。
type IconStore interface {
	// SetIcon はプレイヤーの icon_hash を更新し、旧 data（player_icons）にも画像を保存する（同じ内容は重複して保存しない）。
	// プレイヤーがいなければ ErrNotFound。
	SetIcon(ctx context.Context, playerID, hash string, data []byte) (*Player, error)
	// GetIconHash はプレイヤーの現在の icon_hash を返す。プレイヤーがいない、またはアイコンが無ければ ErrNotFound。
	GetIconHash(ctx context.Context, playerID string) (string, error)
	// LegacyIconData は旧 data（player_icons）から画像を読み出す。blob に無いときのフォールバック。無ければ ErrNotFound。
	LegacyIconData(ctx context.Context, hash string) ([]byte, error)
}

// IconLister は旧 data を hash の昇順で走査する（cmd/iconmigrate 用）。
type IconLister interface {
	// ListIcons は afterHash より大きい hash のアイコンを、hash の昇順に最大 limit 件返す。
	ListIcons(ctx context.Context, afterHash string, limit int) ([]PlayerIcon, error)
}

func (s *GormStore) SetIcon(ctx context.Context, playerID, hash string, data []byte) (*Player, error) {
	var p Player
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&p, "id = ?", playerID).Error; err != nil {
			return mapNotFound(err)
		}
		icon := PlayerIcon{Hash: hash, Data: data, CreatedAt: time.Now().UTC()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&icon).Error; err != nil {
			return err
		}
		p.IconHash = &hash
		return tx.Model(&p).Update("icon_hash", hash).Error
	})
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *GormStore) GetIconHash(ctx context.Context, playerID string) (string, error) {
	var p Player
	if err := s.db.WithContext(ctx).Select("icon_hash").First(&p, "id = ?", playerID).Error; err != nil {
		return "", mapNotFound(err)
	}
	if p.IconHash == nil {
		return "", ErrNotFound
	}
	return *p.IconHash, nil
}

func (s *GormStore) LegacyIconData(ctx context.Context, hash string) ([]byte, error) {
	var icon PlayerIcon
	if err := s.db.WithContext(ctx).First(&icon, "hash = ?", hash).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return icon.Data, nil
}

func (s *GormStore) ListIcons(ctx context.Context, afterHash string, limit int) ([]PlayerIcon, error) {
	var icons []PlayerIcon
	err := s.db.WithContext(ctx).Where("hash > ?", afterHash).Order("hash ASC").Limit(limit).Find(&icons).Error
	return icons, err
}
