// gameserver は 1 ルーム分のリアルタイムサーバ（Agones の GameServer になる）。
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

	"github.com/o-ga09/gopher-office/internal/gameserver"
)

func main() {
	var (
		addr        = flag.String("addr", ":7654", "listen address")
		local       = flag.Bool("local", false, "ローカル開発用。トークンを検証せず、Agones SDK も使わない")
		roomID      = flag.String("room-id", "local", "room id (for logs)")
		maxPlayers  = flag.Int("max-players", 200, "入室人数の上限")
		idleTimeout = flag.Duration("idle-timeout", 0, "無人がこの時間続いたら終了する。0 なら終了しない")
		static      = flag.String("static", "", "指定したディレクトリを / で静的配信する（ブラウザ版の動作確認用）")
		origins     = flag.String("origin", "", "WebSocket で追加で許可する Origin の host パターン（カンマ区切り）")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 本番用のトークン検証は API（P2b）と一緒に入れる。それまでは誤って無認証で公開しないよう、-local を必須にする
	if !*local {
		log.Fatal("token verification is not implemented yet: run with -local")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	originPatterns := []string{"localhost:*", "127.0.0.1:*"}
	for _, o := range strings.Split(*origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			originPatterns = append(originPatterns, o)
		}
	}

	s := gameserver.NewServer(gameserver.Config{
		RoomID:         *roomID,
		MaxPlayers:     *maxPlayers,
		IdleTimeout:    *idleTimeout,
		OnIdle:         stop,
		Verifier:       gameserver.InsecureVerifier{},
		Logger:         logger,
		OriginPatterns: originPatterns,
	})

	mux := http.NewServeMux()
	h := s.Handler()
	mux.Handle("/room", h)
	mux.Handle("/healthz", h)
	if *static != "" {
		mux.Handle("/", http.FileServer(http.Dir(*static)))
	}

	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	go s.Room().Run(ctx)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info("listening", "addr", *addr, "local", *local, "max_players", *maxPlayers)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	logger.Info("stopped")
}
