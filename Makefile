.PHONY: run wasm serve test

run: ## デスクトップ版を起動
	go run ./cmd/client

wasm: ## ブラウザ版をビルド（web/ に出力）
	GOOS=js GOARCH=wasm go build -o web/game.wasm ./cmd/client
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" web/

serve: wasm ## ブラウザ版をビルドして http://localhost:8080 で配信
	cd web && python3 -m http.server 8080

test:
	go test ./...
