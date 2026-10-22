// ctl は運用者が使う小さなコマンド。いまは、ワークスペースを作って招待 URL を表示する。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/o-ga09/gopher-office/internal/apiclient"
)

func main() {
	var (
		api  = flag.String("api", "http://localhost:8080", "API のベース URL")
		name = flag.String("name", "Gopher Office", "ワークスペースの名前")
		base = flag.String("base-url", "http://localhost:7654", "招待 URL のオリジン（ブラウザ版を配信している所）")
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cl := apiclient.New(*api)
	if _, err := cl.CreateGuest(ctx, "admin", 2); err != nil {
		log.Fatal("create the owner: ", err)
	}
	ws, err := cl.CreateWorkspace(ctx, *name)
	if err != nil {
		log.Fatal("create the workspace: ", err)
	}
	fmt.Printf("%s%s\n", strings.TrimRight(*base, "/"), ws.InvitePath)
}
