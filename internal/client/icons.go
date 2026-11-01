package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/png" // 配信されるアイコンは PNG
	"time"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/o-ga09/gopher-office/internal/apiclient"
	"github.com/o-ga09/gopher-office/internal/icon"
)

// maxIconFetches はアイコンを同時に取得する数の上限。数百人が一斉に入っても、取得が集中しないようにする。
const maxIconFetches = 4

type iconState int

const (
	iconNone    iconState = iota // アイコンが無い（または取得に失敗した）
	iconLoading                  // 取得中。シルエットを表示する
	iconReady
)

type iconResult struct {
	hash string
	img  image.Image
	err  error
}

// iconCache はプレイヤーのアイコン画像を、ハッシュごとにメモリに保持する。
// 取得は API から非同期に行い、画像の生成（ebiten.Image）はゲームループで行う。
// 同じハッシュは 1 回だけ取得する。失敗したハッシュは、再接続するまで取り直さない。
type iconCache struct {
	apiBase  string
	imgs     map[string]*ebiten.Image
	fetching map[string]bool // 取得を始めた（完了したものを含む）
	failed   map[string]bool // 取得に失敗した
	sem      chan struct{}
	results  chan iconResult
}

func newIconCache(apiBase string) *iconCache {
	return &iconCache{
		apiBase:  apiBase,
		imgs:     make(map[string]*ebiten.Image),
		fetching: make(map[string]bool),
		failed:   make(map[string]bool),
		sem:      make(chan struct{}, maxIconFetches),
		results:  make(chan iconResult, 256),
	}
}

// get はアイコンの状態を返す。未取得なら取得を始める（このとき iconLoading）。
// playerID はそのアイコンを持つプレイヤーで、取得先の URL に使う。
func (c *iconCache) get(playerID, hash string) (*ebiten.Image, iconState) {
	if c.apiBase == "" || !icon.ValidHash(hash) {
		return nil, iconNone
	}
	if img, ok := c.imgs[hash]; ok {
		return img, iconReady
	}
	if c.failed[hash] {
		return nil, iconNone
	}
	if !c.fetching[hash] {
		c.fetching[hash] = true
		go c.fetch(playerID, hash)
	}
	return nil, iconLoading
}

func (c *iconCache) fetch(playerID, hash string) {
	c.sem <- struct{}{}
	defer func() { <-c.sem }()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	data, err := apiclient.New(c.apiBase).Icon(ctx, playerID, hash)
	if err == nil {
		// 別の画像が返ってきたとき（アイコンを変えた直後など）に、違う人のアイコンを表示しない
		if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != hash {
			err = errHashMismatch
		}
	}
	var img image.Image
	if err == nil {
		img, _, err = image.Decode(bytes.NewReader(data))
	}
	c.results <- iconResult{hash: hash, img: img, err: err}
}

var errHashMismatch = errors.New("icon: hash mismatch")

// update は取得が終わったアイコンを取り込む。ゲームループ（Update）から呼ぶ。
func (c *iconCache) update() {
	for {
		select {
		case r := <-c.results:
			if r.err != nil {
				c.failed[r.hash] = true
				continue
			}
			c.imgs[r.hash] = ebiten.NewImageFromImage(r.img)
		default:
			return
		}
	}
}
