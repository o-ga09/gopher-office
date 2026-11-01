// gateway は、クライアントの WebSocket を、roomToken が指す GameServer に中継する。
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

	"github.com/o-ga09/gopher-office/internal/auth"
	"github.com/o-ga09/gopher-office/internal/gateway"
	"github.com/o-ga09/gopher-office/internal/k8s"
)

func main() {
	var (
		addr      = flag.String("addr", ":8081", "listen address")
		namespace = flag.String("namespace", "gopher-office", "GameServer の Namespace")
		gsPort    = flag.String("gameserver-port", "7654", "GameServer のポート")
		maxConns  = flag.Int("max-conns", 5000, "同時に中継する接続数の上限")
		origins   = flag.String("origin", "", "WebSocket で許可する Origin の host パターン（カンマ区切り。例: office.o-ga09.com）。空なら同一オリジンのみ")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 検証鍵は環境変数で受け取る。ゲートウェイは roomToken の鍵だけを持つ（playerToken は扱えない）
	secret := os.Getenv("ROOM_TOKEN_SECRET")
	if secret == "" {
		log.Fatal("environment variable ROOM_TOKEN_SECRET is required")
	}
	verifier, err := auth.NewRoomVerifier(secret, nil)
	if err != nil {
		log.Fatal(err)
	}
	kube, err := k8s.InCluster()
	if err != nil {
		log.Fatal(err)
	}

	var patterns []string
	for _, o := range strings.Split(*origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			patterns = append(patterns, o)
		}
	}
	gw, err := gateway.New(gateway.Config{
		Verifier:       verifier,
		Resolver:       &gateway.KubeResolver{Kube: kube, Namespace: *namespace, Port: *gsPort},
		Logger:         logger,
		OriginPatterns: patterns,
		MaxConns:       *maxConns,
	})
	if err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{Addr: *addr, Handler: gw.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	logger.Info("listening", "addr", *addr, "namespace", *namespace, "origins", patterns)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	logger.Info("stopped")
}
