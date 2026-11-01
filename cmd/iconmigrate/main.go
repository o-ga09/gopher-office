// iconmigrate は player_icons.data のアイコン画像をオブジェクトストレージへ移す（docs/design.md §8 の段階 B）。
// sha256(data) が hash と一致するものだけを icons/<hash>.png に Put する。冪等なので何度でも実行できる。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/o-ga09/gopher-office/internal/api"
	"github.com/o-ga09/gopher-office/internal/blob"
	"github.com/o-ga09/gopher-office/internal/blob/s3blob"
)

func main() {
	var (
		store  = flag.String("icon-store", "s3", "移行先: dir | s3")
		dir    = flag.String("icon-dir", "./icons-data", "-icon-store=dir のときのディレクトリ")
		dryRun = flag.Bool("dry-run", false, "書き込まずに、移行される件数だけを数える")
	)
	flag.Parse()

	dst, err := openBlob(*store, *dir)
	if err != nil {
		log.Fatal(err)
	}
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		log.Fatal("environment variable MYSQL_DSN is required")
	}
	db, conn, err := api.OpenMySQL(dsn)
	if err != nil {
		log.Fatal(err) // DSN（パスワードを含む）は出力しない
	}
	defer func() { _ = conn.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res, err := api.MigrateIcons(ctx, api.NewGormStore(db), dst, *dryRun, log.Printf)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("scanned=%d put=%d mismatch=%d dry_run=%t\n", res.Scanned, res.Put, res.Mismatch, *dryRun)
}

func openBlob(kind, dir string) (blob.Store, error) {
	switch kind {
	case "dir":
		return blob.NewDir(dir)
	case "s3":
		return s3blob.New(s3blob.ConfigFromEnv(os.Getenv))
	}
	return nil, fmt.Errorf("-icon-store must be dir or s3, got %q", kind)
}
