package api

import (
	"database/sql"
	"fmt"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// NewGormDB は、既存の *sql.DB から GORM の DB を作る。
func NewGormDB(conn *sql.DB) (*gorm.DB, error) {
	return gorm.Open(mysql.New(mysql.Config{Conn: conn}), &gorm.Config{
		// SQL とそのパラメータ（表示名など）をログに出さない
		Logger:                 logger.Discard,
		NowFunc:                func() time.Time { return time.Now().UTC() },
		SkipDefaultTransaction: true, // 単発の書き込みで暗黙のトランザクションを張らない（必要な所で明示する）
	})
}

// OpenMySQL は DSN で MySQL に接続し、接続できることを確認する。
// DSN には parseTime=true と charset=utf8mb4 を含めること（例: user:pass@tcp(host:3306)/db?parseTime=true&charset=utf8mb4）。
func OpenMySQL(dsn string) (*gorm.DB, *sql.DB, error) {
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("api: open mysql: %w", err)
	}
	conn.SetMaxOpenConns(20)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(30 * time.Minute)
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("api: ping mysql: %w", err)
	}
	db, err := NewGormDB(conn)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return db, conn, nil
}
