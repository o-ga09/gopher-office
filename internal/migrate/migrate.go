// Package migrate は埋め込んだ SQL を順に適用する、最小のマイグレーション。
//
// GORM の AutoMigrate は使わない。起動のたびにスキーマを書き換えると、複数レプリカや Fleet と相性が悪いため、
// マイグレーションは専用の Job（cmd/migrate）で 1 回だけ実行する。
//
// MySQL の DDL は暗黙にコミットされ、ファイルの途中で失敗すると一部だけ適用された状態になる。
// そのため 1 ファイルには少数の独立した文だけを書き、失敗したら手で直す前提にする。
package migrate

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed sql/*.sql
var files embed.FS

const lockName = "gopher_office_migrate"

// Up は未適用のマイグレーションをすべて適用し、適用したバージョンを返す。
// 複数のプロセスから同時に呼ばれても、MySQL のロックで 1 つずつ実行される。
func Up(ctx context.Context, db *sql.DB) ([]string, error) {
	// GET_LOCK は接続ごとのロックなので、1 本の接続を使い続ける
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	var got int
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 30)", lockName).Scan(&got); err != nil {
		return nil, fmt.Errorf("migrate: lock: %w", err)
	}
	if got != 1 {
		return nil, errors.New("migrate: could not acquire the migration lock")
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "SELECT RELEASE_LOCK(?)", lockName) }()

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    VARCHAR(64) NOT NULL,
		applied_at DATETIME(3) NOT NULL,
		PRIMARY KEY (version)
	) ENGINE=InnoDB DEFAULT CHARSET=ascii`); err != nil {
		return nil, fmt.Errorf("migrate: create schema_migrations: %w", err)
	}

	done, err := appliedVersions(ctx, conn)
	if err != nil {
		return nil, err
	}

	names, err := fs.Glob(files, "sql/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)

	var applied []string
	for _, name := range names {
		version := strings.TrimSuffix(strings.TrimPrefix(name, "sql/"), ".sql")
		if done[version] {
			continue
		}
		body, err := files.ReadFile(name)
		if err != nil {
			return applied, err
		}
		for i, stmt := range splitStatements(string(body)) {
			if _, err := conn.ExecContext(ctx, stmt); err != nil {
				return applied, fmt.Errorf("migrate: %s: statement %d: %w", version, i+1, err)
			}
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, NOW(3))", version); err != nil {
			return applied, fmt.Errorf("migrate: record %s: %w", version, err)
		}
		applied = append(applied, version)
	}
	return applied, nil
}

func appliedVersions(ctx context.Context, conn *sql.Conn) (map[string]bool, error) {
	rows, err := conn.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("migrate: list applied: %w", err)
	}
	defer func() { _ = rows.Close() }()
	done := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		done[v] = true
	}
	return done, rows.Err()
}

// splitStatements は SQL ファイルを文に分ける。`--` で始まる行は捨て、行末の `;` で区切る。
// 文字列リテラルの中の `;` は想定しない（マイグレーションには書かない）。
func splitStatements(src string) []string {
	var (
		stmts []string
		cur   strings.Builder
	)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			stmts = append(stmts, s)
		}
		cur.Reset()
	}
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i] // 行末のコメントも落とす
		}
		line = strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		cur.WriteString(line)
		cur.WriteString("\n")
		if strings.HasSuffix(line, ";") {
			s := strings.TrimSuffix(strings.TrimSpace(cur.String()), ";")
			cur.Reset()
			cur.WriteString(s)
			flush()
		}
	}
	flush()
	return stmts
}
