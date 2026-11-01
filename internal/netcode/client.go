package netcode

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	officev1 "github.com/o-ga09/gopher-office/gen/office/v1"
	"github.com/o-ga09/gopher-office/internal/wire"
)

const (
	pingInterval = time.Second // Cloudflare のアイドルタイムアウト（100 秒）対策と RTT 計測を兼ねる
	writeTimeout = 5 * time.Second
	recvBuffer   = 1024
	sendBuffer   = 64
	// maxServerFrame はサーバからの 1 フレームの上限。Welcome は人数に比例して大きくなる。
	maxServerFrame = 4 << 20
)

// ErrClosed は接続が閉じたことを表す。
var ErrClosed = errors.New("netcode: connection closed")

// Client は GameServer との WebSocket 接続。ブラウザ（WASM）でもネイティブでも動く。
// Send と Recv はゲームループから呼べるようにブロックしない。
type Client struct {
	ws     *websocket.Conn
	cancel context.CancelFunc
	in     chan *officev1.ServerMessage
	out    chan []byte

	rttNanos  atomic.Int64
	bytesRecv atomic.Int64

	errMu sync.Mutex
	err   error
}

// Dial は url に接続して Hello を送る。以後のメッセージは Recv から受け取る。
func Dial(ctx context.Context, url string, hello *officev1.Hello) (*Client, error) {
	if hello.GetClientVersion() == "" {
		hello.ClientVersion = wire.ProtocolVersion
	}
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(maxServerFrame)

	b, err := wire.Marshal(&officev1.ClientMessage{Body: &officev1.ClientMessage_Hello{Hello: hello}})
	if err == nil {
		err = ws.Write(ctx, websocket.MessageBinary, b)
	}
	if err != nil {
		_ = ws.CloseNow()
		return nil, err
	}

	// 接続の寿命は Dial に渡した ctx から切り離す（ctx は接続を張るまでの待ち時間に使うことが多い）
	loopCtx, cancel := context.WithCancel(context.Background())
	c := &Client{
		ws:     ws,
		cancel: cancel,
		in:     make(chan *officev1.ServerMessage, recvBuffer),
		out:    make(chan []byte, sendBuffer),
	}
	go c.readLoop(loopCtx)
	go c.writeLoop(loopCtx)
	go c.pingLoop(loopCtx)
	return c, nil
}

// Recv は受信したメッセージのチャネルを返す。接続が閉じると、このチャネルも閉じる。
func (c *Client) Recv() <-chan *officev1.ServerMessage { return c.in }

// Err は接続が閉じた理由を返す。閉じていなければ nil。
func (c *Client) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.err
}

// RTT は直近の往復遅延を返す。まだ計測できていなければ 0。
func (c *Client) RTT() time.Duration { return time.Duration(c.rttNanos.Load()) }

// BytesRecv は、これまでに受信したペイロードの合計バイト数を返す（負荷試験の帯域の計測用）。
func (c *Client) BytesRecv() int64 { return c.bytesRecv.Load() }

// Send はメッセージを送信キューに積む。キューが満杯なら、入力を捨てて false を返す。
func (c *Client) Send(m *officev1.ClientMessage) bool {
	b, err := wire.Marshal(m)
	if err != nil {
		return false
	}
	select {
	case c.out <- b:
		return true
	default:
		return false
	}
}

// Close は接続を閉じる。
func (c *Client) Close() {
	c.fail(nil)
	_ = c.ws.CloseNow()
}

func (c *Client) fail(err error) {
	c.errMu.Lock()
	if c.err == nil {
		if err == nil {
			err = ErrClosed
		}
		c.err = err
	}
	c.errMu.Unlock()
	c.cancel()
}

func (c *Client) readLoop(ctx context.Context) {
	defer close(c.in)
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			c.fail(err)
			return
		}
		c.bytesRecv.Add(int64(len(data)))
		msg, err := wire.UnmarshalServer(data)
		if err != nil {
			c.fail(err)
			_ = c.ws.CloseNow()
			return
		}
		if pong := msg.GetPong(); pong != nil {
			c.rttNanos.Store((time.Duration(time.Now().UnixMilli()-pong.GetClientTimeMs()) * time.Millisecond).Nanoseconds())
			continue
		}
		select {
		case c.in <- msg:
		case <-ctx.Done():
			return
		}
	}
}

func (c *Client) writeLoop(ctx context.Context) {
	for {
		select {
		case b := <-c.out:
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.ws.Write(wctx, websocket.MessageBinary, b)
			cancel()
			if err != nil {
				c.fail(err)
				_ = c.ws.CloseNow()
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (c *Client) pingLoop(ctx context.Context) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			c.Send(&officev1.ClientMessage{Body: &officev1.ClientMessage_Ping{Ping: &officev1.Ping{ClientTimeMs: time.Now().UnixMilli()}}})
		case <-ctx.Done():
			return
		}
	}
}
