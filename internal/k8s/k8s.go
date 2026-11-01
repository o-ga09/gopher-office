// Package k8s は、クラスタ内の Pod から Kubernetes API を呼ぶ、最小の REST クライアント。
//
// 必要なのは GameServerAllocation の作成、GameServer と Pod の取得だけなので、client-go（巨大な依存）は
// 使わない。認証は Pod の ServiceAccount のトークンで、権限は RBAC で必要最小限にする。
package k8s

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	saDir        = "/var/run/secrets/kubernetes.io/serviceaccount"
	maxBodyBytes = 1 << 20
)

// ErrNotFound はリソースが無い（404）ことを表す。
var ErrNotFound = errors.New("k8s: not found")

// Client は Kubernetes API のクライアント。
type Client struct {
	base      string
	http      *http.Client
	tokenFile string // 空なら Authorization を付けない（テスト用）
}

// InCluster は ServiceAccount のトークンと CA で、クラスタ内から API に接続するクライアントを作る。
func InCluster() (*Client, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, errors.New("k8s: not running in a cluster (KUBERNETES_SERVICE_HOST is not set)")
	}
	ca, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("k8s: read ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("k8s: invalid ca.crt")
	}
	return &Client{
		base: "https://" + joinHostPort(host, port),
		http: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
		},
		tokenFile: saDir + "/token",
	}, nil
}

// New は URL を指定してクライアントを作る（テスト用。認証は付けない）。
func New(baseURL string) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: 10 * time.Second}}
}

func joinHostPort(host, port string) string {
	if strings.Contains(host, ":") { // IPv6
		return "[" + host + "]:" + port
	}
	return host + ":" + port
}

// Do はリクエストを送り、2xx のレスポンスを out に JSON として読み込む。404 は ErrNotFound。
// body は JSON にする。トークンは期限があるので、毎回ファイルから読む。
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.tokenFile != "" {
		tok, err := os.ReadFile(c.tokenFile)
		if err != nil {
			return fmt.Errorf("k8s: read token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("k8s: %s %s: %s: %s", method, path, resp.Status, truncate(string(data), 300))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
