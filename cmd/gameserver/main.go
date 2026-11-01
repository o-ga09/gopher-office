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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/o-ga09/gopher-office/internal/auth"
	"github.com/o-ga09/gopher-office/internal/gameserver"
)

func main() {
	var (
		addr        = flag.String("addr", ":7654", "listen address")
		local       = flag.Bool("local", false, "ローカル開発用。Agones SDK を使わず、localhost の Origin を許可する。ROOM_TOKEN_SECRET が無ければトークンも検証しない")
		roomID      = flag.String("room-id", "", "このサーバが担当するルームの ID。roomToken がこのルームのものかを検証する。空なら、どのルームのトークンも受け付ける（開発用）")
		maxPlayers  = flag.Int("max-players", 200, "入室人数の上限")
		idleTimeout = flag.Duration("idle-timeout", 0, "無人がこの時間続いたら終了する。0 なら終了しない")
		static      = flag.String("static", "", "指定したディレクトリを / で静的配信する（ブラウザ版の動作確認用）")
		origins     = flag.String("origin", "", "WebSocket で追加で許可する Origin の host パターン（カンマ区切り）")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// roomToken の検証鍵は環境変数で渡す（プロセス一覧やシェルの履歴に残るフラグでは渡さない）。
	// 鍵が無いまま -local なしで起動して、誤って認証なしで公開しないようにする
	var verifier gameserver.TokenVerifier = gameserver.InsecureVerifier{}
	switch secret := os.Getenv("ROOM_TOKEN_SECRET"); {
	case secret != "":
		v, err := auth.NewRoomVerifier(secret, func() string { return *roomID })
		if err != nil {
			log.Fatal(err)
		}
		verifier = v
		if *roomID == "" {
			logger.Warn("-room-id is empty: room tokens for any room are accepted")
		}
	case *local:
		logger.Warn("ROOM_TOKEN_SECRET is not set: room tokens are NOT verified (local mode)")
	default:
		log.Fatal("ROOM_TOKEN_SECRET is required (use -local for development without token verification)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var originPatterns []string
	if *local {
		originPatterns = []string{"localhost:*", "127.0.0.1:*"}
	}
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
		Verifier:       verifier,
		Logger:         logger,
		OriginPatterns: originPatterns,
	})

	mux := http.NewServeMux()
	h := s.Handler()
	mux.Handle("/room", h)
	mux.Handle("/healthz", h)
	if *static != "" {
		mux.Handle("/", http.FileServer(http.Dir(*static)))
		// 招待 URL（/w/{コード}）でも、同じ HTML を返す
		mux.HandleFunc("/w/", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, filepath.Join(*static, "index.html"))
		})
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
