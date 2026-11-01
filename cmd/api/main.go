// api はゲーム API サーバ（認証・プロフィール・ワークスペース・入室）。
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/o-ga09/gopher-office/internal/api"
	"github.com/o-ga09/gopher-office/internal/auth"
	"github.com/o-ga09/gopher-office/internal/k8s"
)

func main() {
	var (
		addr          = flag.String("addr", ":8080", "listen address")
		gameServerURL = flag.String("gameserver-url", "ws://localhost:7654/room", "クライアントが接続する GameServer の WebSocket URL（-agones を使わないときの固定の割り当て）")
		useAgones     = flag.Bool("agones", false, "Agones の GameServerAllocation でルームに GameServer を割り当てる（クラスタ内で動かす。REDIS_URL が必要）")
		agonesNS      = flag.String("agones-namespace", "gopher-office", "GameServer の Namespace（-agones のとき）")
		agonesFleet   = flag.String("agones-fleet", "office", "割り当て対象の Fleet の名前（-agones のとき）")
		publicWSURL   = flag.String("public-ws-url", "", "クライアントが接続する、ゲートウェイの公開 WebSocket URL（例: wss://office.o-ga09.com/room。-agones のとき必須）")
		maxWorkspaces = flag.Int("max-workspaces", 5, "1 人が作れるワークスペースの数")
		guestPerMin   = flag.Int("guest-per-minute", 20, "IP ごとに、ゲストを作れる回数（分あたり）。負荷試験では上げる")
		actionPerMin  = flag.Int("action-per-minute", 60, "プレイヤーごとに、ワークスペースの作成・入室ができる回数（分あたり）")
		trustCF       = flag.Bool("trust-cloudflare-ip", false, "クライアントの IP を CF-Connecting-IP から取る（API に Cloudflare を通らず届かない場合だけ）")
		corsOrigins   = flag.String("cors-origin", "", "別のオリジンのブラウザから呼ぶことを許可するオリジン（カンマ区切り。開発用。本番は同一オリジンなので空）")
	)
	flag.Parse()

	// 秘密の値は、プロセス一覧やシェルの履歴に残るフラグではなく環境変数で受け取る
	dsn := mustEnv("MYSQL_DSN")
	signer, err := auth.NewSigner(mustEnv("PLAYER_TOKEN_SECRET"), mustEnv("ROOM_TOKEN_SECRET"))
	if err != nil {
		log.Fatal(err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	db, conn, err := api.OpenMySQL(dsn)
	if err != nil {
		log.Fatal(err) // DSN（パスワードを含む）は出力しない
	}
	defer func() { _ = conn.Close() }()

	var origins []string
	for _, o := range strings.Split(*corsOrigins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}

	var allocator api.RoomAllocator = api.StaticAllocator{URL: *gameServerURL}
	if *useAgones {
		if *publicWSURL == "" {
			log.Fatal("-public-ws-url is required with -agones")
		}
		opts, err := redis.ParseURL(mustEnv("REDIS_URL"))
		if err != nil {
			log.Fatal("REDIS_URL is invalid") // 値（パスワードを含みうる）は出力しない
		}
		kube, err := k8s.InCluster()
		if err != nil {
			log.Fatal(err)
		}
		rc := redis.NewClient(opts)
		defer func() { _ = rc.Close() }()
		allocator = &api.AgonesAllocator{
			Kube: kube, KV: api.NewRedisKV(rc), Namespace: *agonesNS, Fleet: *agonesFleet, PublicURL: *publicWSURL,
		}
	}

	e, err := api.NewServer(api.Config{
		Store:                  api.NewGormStore(db),
		Signer:                 signer,
		Allocator:              allocator,
		Logger:                 logger,
		MaxWorkspacesPerPlayer: *maxWorkspaces,
		GuestPerMinute:         *guestPerMin,
		ActionPerMinute:        *actionPerMin,
		TrustCloudflareIP:      *trustCF,
		CORSOrigins:            origins,
	})
	if err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           e,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info("listening", "addr", *addr, "cors_origins", origins)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	logger.Info("stopped")
}

func mustEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("environment variable %s is required", name)
	}
	return v
}
