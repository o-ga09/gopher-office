// Package agones は GameServer が Agones のサイドカー（SDK サーバ）と話すための、最小の REST クライアント。
//
// 公式の Go SDK は gRPC と大きな依存を引き込むので、必要な 4 つの操作（Ready / Health / Shutdown /
// GameServer の監視）だけを、サイドカーの HTTP API（既定 localhost:9358）で行う。
package agones

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// RoomIDAnnotation は、API が GameServerAllocation で付ける、担当するルームの ID の annotation。
	RoomIDAnnotation = "roomId"

	// HealthInterval は Health を送る間隔。Fleet の health.periodSeconds より短くする。
	HealthInterval = 2 * time.Second
)

// Client は Agones SDK サーバのクライアント。
type Client struct {
	base string
	http *http.Client
	// stream は監視（長時間つなぎっぱなし）用で、タイムアウトを付けない。
	stream *http.Client
}

// New は SDK サーバの URL（例: http://localhost:9358）からクライアントを作る。
func New(baseURL string) *Client {
	return &Client{
		base:   strings.TrimRight(baseURL, "/"),
		http:   &http.Client{Timeout: 5 * time.Second},
		stream: &http.Client{},
	}
}

// NewFromEnv は環境変数 AGONES_SDK_HTTP_PORT（サイドカーが GameServer のコンテナに渡す）から作る。
func NewFromEnv() *Client {
	port := os.Getenv("AGONES_SDK_HTTP_PORT")
	if port == "" {
		port = "9358"
	}
	return New("http://localhost:" + port)
}

func (c *Client) post(ctx context.Context, path string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("agones sdk %s: %s: %s", path, resp.Status, strings.TrimSpace(string(data)))
	}
	return nil
}

// Ready は GameServer が接続を受け付けられる状態になったことを知らせる。
func (c *Client) Ready(ctx context.Context) error { return c.post(ctx, "/ready", struct{}{}) }

// Shutdown は GameServer を終了させる（Fleet が代わりを補充する）。
func (c *Client) Shutdown(ctx context.Context) error { return c.post(ctx, "/shutdown", struct{}{}) }

// Health は 1 回の生存通知を送る。
func (c *Client) Health(ctx context.Context) error { return c.post(ctx, "/health", struct{}{}) }

// HealthLoop は ctx が終わるまで、一定間隔で Health を送る。失敗はログに残して続ける
// （送れない状態が続けば、Agones が GameServer を Unhealthy にする）。
func (c *Client) HealthLoop(ctx context.Context, log *slog.Logger) {
	t := time.NewTicker(HealthInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := c.Health(ctx); err != nil && ctx.Err() == nil {
				log.Warn("agones health failed", "err", err)
			}
		}
	}
}

// State は GameServer の、このプロセスが知りたい情報。
type State struct {
	// Status は Ready / Allocated / Shutdown など。
	Status string
	// RoomID は割り当て時に付いた annotation（RoomIDAnnotation）。未割り当てなら空。
	RoomID string
}

type gameServerJSON struct {
	ObjectMeta struct {
		Annotations map[string]string `json:"annotations"`
	} `json:"object_meta"`
	Status struct {
		State string `json:"state"`
	} `json:"status"`
}

// Watch は GameServer の変化を監視し、変化のたびに fn を呼ぶ。サイドカーとの接続が切れたら、
// 少し待って張り直す。ctx が終わるまで戻らない。
func (c *Client) Watch(ctx context.Context, log *slog.Logger, fn func(State)) {
	for ctx.Err() == nil {
		if err := c.watchOnce(ctx, fn); err != nil && ctx.Err() == nil {
			log.Warn("agones watch ended", "err", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

func (c *Client) watchOnce(ctx context.Context, fn func(State)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/watch/gameserver", nil)
	if err != nil {
		return err
	}
	resp, err := c.stream.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agones sdk watch: %s", resp.Status)
	}

	// 1 行に 1 つの JSON（{"result": {...}}）が流れてくる
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg struct {
			Result gameServerJSON `json:"result"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			return fmt.Errorf("agones sdk watch: bad message: %w", err)
		}
		fn(State{Status: msg.Result.Status.State, RoomID: msg.Result.ObjectMeta.Annotations[RoomIDAnnotation]})
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("agones sdk watch: closed by the sidecar")
}
