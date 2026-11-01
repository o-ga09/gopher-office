package migrate

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/o-ga09/gopher-office/internal/testutil"
)

func TestSplitStatements(t *testing.T) {
	src := `-- 先頭のコメント
CREATE TABLE a (
  id INT NOT NULL, -- 行末のコメント
  PRIMARY KEY (id)
) ENGINE=InnoDB;

-- 2 つ目
CREATE TABLE b (id INT);
`
	want := []string{
		"CREATE TABLE a (\n  id INT NOT NULL,\n  PRIMARY KEY (id)\n) ENGINE=InnoDB",
		"CREATE TABLE b (id INT)",
	}
	if got := splitStatements(src); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if got := splitStatements("-- only a comment\n\n"); len(got) != 0 {
		t.Fatalf("comment-only file must yield no statements, got %q", got)
	}
	if got := splitStatements("SELECT 1"); !reflect.DeepEqual(got, []string{"SELECT 1"}) {
		t.Fatalf("a final statement without a semicolon must be kept, got %q", got)
	}
}

func TestUpAppliesOnceAndIsIdempotent(t *testing.T) {
	db := testutil.OpenMySQL(t)
	ctx := context.Background()

	applied, err := Up(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) == 0 || applied[0] != "0001_init" {
		t.Fatalf("applied = %v", applied)
	}
	for _, table := range []string{"players", "workspaces", "rooms", "schema_migrations"} {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s missing (n=%d err=%v)", table, n, err)
		}
	}

	again, err := Up(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second run must apply nothing, got %v", again)
	}
}

func TestUpIsSafeUnderConcurrency(t *testing.T) {
	db := testutil.OpenMySQL(t)
	ctx := context.Background()

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		total   int
		runErrs []error
	)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			applied, err := Up(ctx, db)
			mu.Lock()
			defer mu.Unlock()
			total += len(applied)
			if err != nil {
				runErrs = append(runErrs, err)
			}
		}()
	}
	wg.Wait()
	if len(runErrs) > 0 {
		t.Fatalf("concurrent Up failed: %v", runErrs)
	}
	if total != 1 {
		t.Fatalf("exactly one runner must apply 0001_init, applied %d times in total", total)
	}
}

func TestSchemaConstraints(t *testing.T) {
	db := testutil.OpenMySQL(t)
	if _, err := Up(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) error { _, err := db.Exec(q, args...); return err }

	if err := exec("INSERT INTO players (id, display_name, gender, created_at) VALUES ('P1', '🐹 たいち', 1, NOW(3))"); err != nil {
		t.Fatalf("emoji and Japanese names must be storable (utf8mb4): %v", err)
	}

	// 招待コードは大文字小文字を区別する
	ins := func(id, code string) error {
		return exec("INSERT INTO workspaces (id, owner_id, name, invite_code, created_at) VALUES (?, 'P1', 'w', ?, NOW(3))", id, code)
	}
	if err := ins("W1", "aBcDeFgHiJkL"); err != nil {
		t.Fatal(err)
	}
	if err := ins("W2", "abcdefghijkl"); err != nil {
		t.Fatalf("codes differing only in case must not collide: %v", err)
	}
	if err := ins("W3", "aBcDeFgHiJkL"); err == nil {
		t.Fatal("an identical invite code must be rejected")
	}

	// 外部キー
	if err := exec("INSERT INTO workspaces (id, owner_id, name, invite_code, created_at) VALUES ('W9', 'NOBODY', 'w', 'zzzzzzzzzzzz', NOW(3))"); err == nil {
		t.Fatal("a workspace without an owner must be rejected")
	}
	if err := exec("INSERT INTO rooms (id, workspace_id, name) VALUES ('R1', 'NOPE', 'r')"); err == nil {
		t.Fatal("a room without a workspace must be rejected")
	}
	if err := exec("INSERT INTO rooms (id, workspace_id, name) VALUES ('R1', 'W1', 'r')"); err != nil {
		t.Fatal(err)
	}
	var mapID string
	if err := db.QueryRow("SELECT map_id FROM rooms WHERE id = 'R1'").Scan(&mapID); err != nil || mapID != "office-1" {
		t.Fatalf("map_id default = %q (%v)", mapID, err)
	}
}
