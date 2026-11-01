// gameserver は 1 ルーム分のリアルタイムサーバ（Agones の GameServer になる）。
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/o-ga09/gopher-office/internal/agones"
	"github.com/o-ga09/gopher-office/internal/auth"
	"github.com/o-ga09/gopher-office/internal/gameserver"
)

const (
	// defaultAgonesIdle は、Agones で割り当てられたあと、無人がこの時間続いたら終了する既定値。
	defaultAgonesIdle = 5 * time.Minute
	// unallocatedRoom は、割り当て前の GameServer が担当するルームの ID。どのトークンの room とも一致しない。
	unallocatedRoom = "-unallocated-"
)

func main() {
	var (
		addr        = flag.String("addr", ":7654", "listen address")
		local       = flag.Bool("local", false, "ローカル開発用。Agones SDK を使わず、localhost の Origin を許可する。ROOM_TOKEN_SECRET が無ければトークンも検証しない")
		roomID      = flag.String("room-id", "", "このサーバが担当するルームの ID。roomToken がこのルームのものかを検証する。空なら、Agones では割り当て時の annotation から決まり（それまでは誰も入れない）、-local ではどのルームのトークンも受け付ける")
		maxPlayers  = flag.Int("max-players", 200, "入室人数の上限")
		idleTimeout = flag.Duration("idle-timeout", 0, "無人がこの時間続いたら終了する。0 なら、-local では終了せず、Agones では 5 分（割り当てられてから数える）")
		static      = flag.String("static", "", "指定したディレクトリを / で静的配信する（ブラウザ版の動作確認用）")
		origins     = flag.String("origin", "", "WebSocket で追加で許可する Origin の host パターン（カンマ区切り）")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	useAgones := !*local
	if useAgones && *idleTimeout == 0 {
		*idleTimeout = defaultAgonesIdle
	}
	// 担当するルームの ID。Agones では、割り当てのときに付く annotation から決まる
	var allocatedRoom atomic.Value
	currentRoom := func() string {
		if *roomID != "" {
			return *roomID
		}
		if useAgones {
			if id, _ := allocatedRoom.Load().(string); id != "" {
				return id
			}
			return unallocatedRoom // 割り当て前は、どのトークンも通さない
		}
		return ""
	}

	// roomToken の検証鍵は環境変数で渡す（プロセス一覧やシェルの履歴に残るフラグでは渡さない）。
	// 鍵が無いまま -local なしで起動して、誤って認証なしで公開しないようにする
	var verifier gameserver.TokenVerifier = gameserver.InsecureVerifier{}
	switch secret := os.Getenv("ROOM_TOKEN_SECRET"); {
	case secret != "":
		v, err := auth.NewRoomVerifier(secret, currentRoom)
		if err != nil {
			log.Fatal(err)
		}
		verifier = v
		if *roomID == "" && !useAgones {
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

	var sdk *agones.Client
	if useAgones {
		sdk = agones.NewFromEnv()
	}
	onIdle := func() {
		if sdk != nil {
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := sdk.Shutdown(sctx); err != nil {
				logger.Error("agones shutdown failed", "err", err)
			}
		}
		stop()
	}

	s := gameserver.NewServer(gameserver.Config{
		RoomID:         *roomID,
		MaxPlayers:     *maxPlayers,
		IdleTimeout:    *idleTimeout,
		DeferIdle:      useAgones,
		OnIdle:         onIdle,
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

	// 先にポートを開いてから Ready にする（Ready の直後に割り当てられても受けられるように）
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}

	go s.Room().Run(ctx)
	if sdk != nil {
		go sdk.HealthLoop(ctx, logger)
		go sdk.Watch(ctx, logger, func(st agones.State) {
			if st.RoomID != "" && allocatedRoom.Load() == nil {
				allocatedRoom.Store(st.RoomID)
				s.Room().Arm()
				logger.Info("allocated", "room", st.RoomID, "state", st.Status)
			}
		})
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := sdk.Ready(rctx)
		cancel()
		if err != nil {
			log.Fatalf("agones ready: %v", err)
		}
		logger.Info("agones: ready")
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info("listening", "addr", *addr, "local", *local, "max_players", *maxPlayers)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	logger.Info("stopped")
}
