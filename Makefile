.PHONY: run wasm serve dev gameserver bot proto test test-all dev-env db db-stop migrate api invite dev-invite

# DB（ローカルの Docker）
DB_CONTAINER ?= gopher-office-dev-mysql
DB_PORT      ?= 33306
DB_PASSWORD  ?= devpw

run: ## デスクトップ版を起動（オフライン）。オンラインは go run ./cmd/client -server ws://localhost:7654/room
	go run ./cmd/client

wasm: ## ブラウザ版をビルド（web/ に出力）
	GOOS=js GOARCH=wasm go build -o web/game.wasm ./cmd/client
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" web/

serve: wasm ## ブラウザ版（オフライン）を http://localhost:8080 で配信
	cd web && python3 -m http.server 8080

dev: wasm ## ブラウザ版と GameServer（認証なし）を起動する。http://localhost:7654/?online=1 を開く
	go run ./cmd/gameserver -local -static web

gameserver: ## ローカル用の GameServer だけを起動（認証なし）
	go run ./cmd/gameserver -local

bot: ## bot を直接つなぐ（N=人数。既定 50）。招待コード経由は go run ./cmd/bot -api ... -invite ...
	go run ./cmd/bot -n $(or $(N),50)

proto: ## proto から Go のコードを生成する（protoc と protoc-gen-go が必要）
	protoc -I proto --go_out=gen --go_opt=module=github.com/o-ga09/gopher-office/gen proto/office/v1/office.proto

test:
	go test ./...

test-all: ## DB を使うテストも含めて実行する（make db が必要）
	TEST_MYSQL_DSN='root:$(DB_PASSWORD)@tcp(127.0.0.1:$(DB_PORT))/' go test -race ./...

# --- 招待 URL で入る構成（API + MySQL + GameServer）の起動手順 ---
#   make db            MySQL を起動（Docker）
#   make migrate       テーブルを作る
#   make api           API を起動（ターミナル 1）
#   make dev-invite    GameServer と、ブラウザ版の配信を起動（ターミナル 2。roomToken を検証する）
#   make invite        ワークスペースを作って、招待 URL を表示する → ブラウザで開く
#   （URL の末尾に ?api=http://localhost:8080 を付ける。ブラウザ版と API のオリジンが違うため）

.env.dev:
	@umask 077 && printf "MYSQL_DSN='%s'\nPLAYER_TOKEN_SECRET='%s'\nROOM_TOKEN_SECRET='%s'\n" \
		"root:$(DB_PASSWORD)@tcp(127.0.0.1:$(DB_PORT))/gopher_office?parseTime=true&charset=utf8mb4" \
		"$$(openssl rand -hex 32)" "$$(openssl rand -hex 32)" > .env.dev
	@echo "created .env.dev (random secrets; not committed)"

dev-env: .env.dev ## 開発用の秘密鍵と DSN を .env.dev に作る（無ければ）

db: ## 開発用の MySQL を Docker で起動する（127.0.0.1 のみに公開）
	@docker start $(DB_CONTAINER) >/dev/null 2>&1 || docker run -d --name $(DB_CONTAINER) \
		-e MYSQL_ROOT_PASSWORD=$(DB_PASSWORD) -e MYSQL_DATABASE=gopher_office \
		-p 127.0.0.1:$(DB_PORT):3306 mysql:8.4
	@echo "MySQL: 127.0.0.1:$(DB_PORT) (起動まで数十秒かかる)"

db-stop: ## 開発用の MySQL を止めて削除する
	-docker rm -f $(DB_CONTAINER)

migrate: .env.dev ## マイグレーションを適用する
	set -a; . ./.env.dev; set +a; go run ./cmd/migrate

api: .env.dev ## API を起動する（ブラウザ版の :7654 からの呼び出しを許可）
	set -a; . ./.env.dev; set +a; go run ./cmd/api -cors-origin http://localhost:7654

dev-invite: .env.dev wasm ## GameServer（roomToken を検証）とブラウザ版の配信を起動する
	set -a; . ./.env.dev; set +a; go run ./cmd/gameserver -local -static web

invite: ## ワークスペースを作って招待 URL を表示する（NAME=名前）
	go run ./cmd/ctl -name "$(or $(NAME),Gopher Office)"
