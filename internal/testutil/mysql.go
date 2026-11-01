// Package testutil はテスト用の補助（実際の MySQL に接続する）。
package testutil

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// EnvMySQLDSN は、テスト用 MySQL の DSN を渡す環境変数。未設定なら MySQL を使うテストはスキップする。
// 例: root:testpw@tcp(127.0.0.1:33306)/
const EnvMySQLDSN = "TEST_MYSQL_DSN"

// OpenMySQL は、テストごとに専用のデータベースを作って接続する。テスト終了時に削除する。
// DSN にはデータベースを指定せず（`/` で終える）、CREATE DATABASE 権限のあるユーザーを使う。
func OpenMySQL(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv(EnvMySQLDSN)
	if dsn == "" {
		t.Skipf("%s is not set; skipping the MySQL test", EnvMySQLDSN)
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("bad %s: %v", EnvMySQLDSN, err)
	}
	cfg.ParseTime = true
	cfg.Loc = nil
	cfg.Params = map[string]string{"charset": "utf8mb4"}

	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	name := "t_" + hex.EncodeToString(b[:])
	if _, err := admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4"); err != nil {
		_ = admin.Close()
		t.Fatalf("create database: %v", err)
	}

	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.Exec("DROP DATABASE IF EXISTS `" + strings.ReplaceAll(name, "`", "") + "`")
		_ = admin.Close()
	})
	return db
}
