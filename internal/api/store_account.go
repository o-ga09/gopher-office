package api

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// Credential は永続アカウントの認証情報。Email は trim + 小文字化済み。
type Credential struct {
	PlayerID     string    `gorm:"primaryKey;size:26"`
	Email        string    `gorm:"size:254;not null"`
	PasswordHash string    `gorm:"size:255;not null"`
	CreatedAt    time.Time `gorm:"precision:3;not null"`
	UpdatedAt    time.Time `gorm:"precision:3;not null"`
}

func (Credential) TableName() string { return "player_credentials" }

// AccountStore はアカウント（メール + パスワード）の永続化。
type AccountStore interface {
	// CreateCredential は認証情報を作る。メールまたはプレイヤーが登録済みなら ErrDuplicate。
	CreateCredential(ctx context.Context, c *Credential) error
	GetCredential(ctx context.Context, playerID string) (*Credential, error)
	GetCredentialByEmail(ctx context.Context, email string) (*Credential, error)
	// ChangePassword はハッシュを差し替え、token_version を 1 上げて、新しい version を返す。
	ChangePassword(ctx context.Context, playerID, newHash string) (uint32, error)
}

func (s *GormStore) CreateCredential(ctx context.Context, c *Credential) error {
	err := s.db.WithContext(ctx).Create(c).Error
	if isDuplicate(err) {
		return ErrDuplicate
	}
	return err
}

func (s *GormStore) GetCredential(ctx context.Context, playerID string) (*Credential, error) {
	var c Credential
	if err := s.db.WithContext(ctx).First(&c, "player_id = ?", playerID).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return &c, nil
}

func (s *GormStore) GetCredentialByEmail(ctx context.Context, email string) (*Credential, error) {
	var c Credential
	if err := s.db.WithContext(ctx).First(&c, "email = ?", email).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return &c, nil
}

func (s *GormStore) ChangePassword(ctx context.Context, playerID, newHash string) (uint32, error) {
	var ver uint32
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec("UPDATE player_credentials SET password_hash = ?, updated_at = ? WHERE player_id = ?", newHash, time.Now().UTC(), playerID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		if err := tx.Exec("UPDATE players SET token_version = token_version + 1 WHERE id = ?", playerID).Error; err != nil {
			return err
		}
		return tx.Raw("SELECT token_version FROM players WHERE id = ?", playerID).Scan(&ver).Error
	})
	return ver, err
}
