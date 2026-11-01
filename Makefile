.PHONY: run wasm serve dev gameserver bot proto test

run: ## デスクトップ版を起動（オフライン）。オンラインは go run ./cmd/client -server ws://localhost:7654/room
	go run ./cmd/client

wasm: ## ブラウザ版をビルド（web/ に出力）
	GOOS=js GOARCH=wasm go build -o web/game.wasm ./cmd/client
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" web/

serve: wasm ## ブラウザ版（オフライン）を http://localhost:8080 で配信
	cd web && python3 -m http.server 8080

dev: wasm ## ブラウザ版と GameServer を起動する。http://localhost:7654/?online=1 を開く
	go run ./cmd/gameserver -local -static web

gameserver: ## ローカル用の GameServer だけを起動
	go run ./cmd/gameserver -local

bot: ## bot を接続する（N=人数。既定 50）
	go run ./cmd/bot -n $(or $(N),50)

proto: ## proto から Go のコードを生成する（protoc と protoc-gen-go が必要）
	protoc -I proto --go_out=gen --go_opt=module=github.com/o-ga09/gopher-office/gen proto/office/v1/office.proto

test:
	go test ./...
