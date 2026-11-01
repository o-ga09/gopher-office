# syntax=docker/dockerfile:1
#
# 1 つの Dockerfile から、サービスごとのイメージを作る（--target で選ぶ）。
#   gameserver / gateway / api（api・migrate・ctl を含む）/ web
# ビルドは常にビルド側のアーキテクチャで行い、Go のクロスコンパイルで amd64 / arm64 を作る（QEMU 不要）。
# ベースイメージはダイジェストで固定する（タグは付け替えられるため）。更新は手動で行う。

FROM --platform=$BUILDPLATFORM golang:1.27@sha256:2920bb124f1a2e12a1d39f0cab756ae0068441ee1da7655dec7f5e374944095b AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/gameserver ./cmd/gateway ./cmd/api ./cmd/migrate ./cmd/ctl

# ブラウザ版（WASM）。ターゲットのアーキテクチャに依存しない
FROM --platform=$BUILDPLATFORM golang:1.27@sha256:2920bb124f1a2e12a1d39f0cab756ae0068441ee1da7655dec7f5e374944095b AS wasm
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /out/web && GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o /out/web/game.wasm ./cmd/client \
    && cp web/index.html "$(go env GOROOT)/lib/wasm/wasm_exec.js" /out/web/

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS gameserver
COPY --from=build /out/gameserver /gameserver
EXPOSE 7654
ENTRYPOINT ["/gameserver"]

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS gateway
COPY --from=build /out/gateway /gateway
EXPOSE 8081
ENTRYPOINT ["/gateway"]

# API。マイグレーション（Job で /migrate を実行する）と運用コマンド（/ctl）も同じイメージに入れる
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS api
COPY --from=build /out/api /api
COPY --from=build /out/migrate /migrate
COPY --from=build /out/ctl /ctl
EXPOSE 8080
ENTRYPOINT ["/api"]

FROM nginxinc/nginx-unprivileged:1.29-alpine@sha256:0c79d56aee561a1d81c63f00eee5fb5fe29279560cdc55e91425133104c7fbe6 AS web
COPY deploy/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=wasm /out/web/ /usr/share/nginx/html/
EXPOSE 8080
