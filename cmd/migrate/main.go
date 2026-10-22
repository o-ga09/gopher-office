// migrate は DB のマイグレーションを適用する（Kubernetes では Job として 1 回だけ実行する）。
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/o-ga09/gopher-office/internal/migrate"
)

func main() {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		log.Fatal("environment variable MYSQL_DSN is required")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal("open mysql: ", err) // DSN（パスワードを含む）は出力しない
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	applied, err := migrate.Up(ctx, db)
	if err != nil {
		log.Fatal(err)
	}
	if len(applied) == 0 {
		fmt.Println("up to date")
		return
	}
	for _, v := range applied {
		fmt.Println("applied", v)
	}
}
