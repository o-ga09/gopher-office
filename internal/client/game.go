// Package client は Ebitengine のクライアント（描画・入力・画面遷移）。ロジックは internal/sim と internal/netcode に置く。
package client

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"sort"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"

	officev1 "github.com/o-ga09/gopher-office/gen/office/v1"
	"github.com/o-ga09/gopher-office/internal/apiclient"
	"github.com/o-ga09/gopher-office/internal/netcode"
	"github.com/o-ga09/gopher-office/internal/profile"
	"github.com/o-ga09/gopher-office/internal/sim"
	"github.com/o-ga09/gopher-office/internal/wire"
)

// Config はクライアントの接続設定。ServerURL も InviteCode も空ならオフライン（自分だけ）で動く。
type Config struct {
	// ServerURL は GameServer の WebSocket URL（例: ws://localhost:7654/room）。API を通さず直接つなぐ（開発用）。
	ServerURL string
	RoomToken string
	Name      string // 表示名。空なら既定名（API 経由では前回の名前）

	// InviteCode を指定すると、API（APIBase）でゲスト登録と入室を行い、返された接続先と roomToken でつなぐ。
	InviteCode string
	APIBase    string     // API のベース URL（例: http://localhost:8080）
	Store      TokenStore // playerToken の保存先。nil なら保存しない（毎回新しいゲストになる）

	// IconPicker はアイコン画像を選ばせる。ブラウザ版だけが実装する（デスクトップ版は nil で、アイコンは設定できない）。
	IconPicker IconPicker
}

// IconPicker は、利用者に画像ファイルを選ばせる。
type IconPicker interface {
	// Pick は選択ダイアログを開く。選ばれたら、PNG に変換して（一辺 256px 以下に縮小して）done を呼ぶ。
	// 取り消されたときは done を呼ばなくてよい。done は別の goroutine から呼んでよい。
	Pick(done func(pngData []byte, err error))
}

// TokenStore は playerToken の保存先。ブラウザでは localStorage を使う。
type TokenStore interface {
	Load() string
	Save(token string)
}

type scene int

const (
	sceneSelect     scene = iota // 性別（Gopher の色）選択
	sceneConnecting              // サーバに接続中
	sceneOffice                  // オフィス
	sceneError                   // 接続失敗・切断
)

// framesPerTick は描画フレーム（60Hz）あたりのシミュレーション tick（20Hz）の比。
const framesPerTick = 60 / sim.TickRate

type dialResult struct {
	c   *netcode.Client
	err error
}

// Game は ebiten.Game の実装。
type Game struct {
	cfg   Config
	scene scene

	sprites [3]*ebiten.Image

	m  *sim.Map
	bg *ebiten.Image

	w        *netcode.World
	conn     *netcode.Client // オフラインのときは nil
	dial     chan dialResult
	err      string // 接続の失敗・切断の理由（利用者向けの文言）
	errTitle string

	frame        int // 現在の tick 内の経過フレーム
	pendingClick *sim.Point
	keys         [4]bool // 今の tick の間に押された移動キー（A, D, W, S）
	lastDirX     int     // サーバに最後に送った方向入力
	lastDirY     int

	hoverCard int
	cardRects [3]image.Rectangle

	nameTags map[nameTagKey]*ebiten.Image
	items    []drawable

	// プロフィール編集（選択画面）
	icons       *iconCache
	nameField   textField
	pickCh      chan pickResult
	profileCh   chan apiclient.Player // 前回のプロフィール（トークンが残っているとき）
	prevProfile *apiclient.Player
	iconPNG     []byte        // 選んだアイコン（PNG）。入室時にアップロードする
	iconPreview *ebiten.Image // 選んだアイコンのプレビュー
	pickErr     string
	fieldRect   image.Rectangle
	iconBtnRect image.Rectangle
}

type drawable struct {
	x, y float64
	p    *netcode.Player
}

// NewGame は選択画面から始まる Game を作る。
func NewGame(cfg Config) *Game {
	g := &Game{
		cfg: cfg, m: sim.OfficeMap(), hoverCard: -1, nameTags: make(map[nameTagKey]*ebiten.Image),
		icons:     newIconCache(cfg.APIBase),
		nameField: textField{max: profile.MaxNameRunes},
		pickCh:    make(chan pickResult, 1),
		profileCh: make(chan apiclient.Player, 1),
	}
	g.nameField.set(cfg.Name)
	g.layoutProfilePanel()
	g.loadPreviousProfile()
	g.bg = newBackground(g.m)
	for i := range g.sprites {
		g.sprites[i] = newGopherSprite(bodyColor(sim.Gender(i)))
	}
	const cardW, cardH, gap = 220, 170, 40
	x0 := (screenW - (3*cardW + 2*gap)) / 2
	for i := range g.cardRects {
		x := x0 + i*(cardW+gap)
		g.cardRects[i] = image.Rect(x, 130, x+cardW, 130+cardH)
	}
	return g
}

func (g *Game) online() bool { return g.cfg.ServerURL != "" || g.cfg.InviteCode != "" }

func (g *Game) Layout(_, _ int) (int, int) { return screenW, screenH }

func (g *Game) Update() error {
	g.icons.update()
	switch g.scene {
	case sceneSelect:
		g.updateSelect()
	case sceneConnecting:
		g.updateConnecting()
	case sceneOffice:
		g.updateOffice()
	case sceneError:
		if _, ok := justClicked(); ok || inpututil.IsKeyJustPressed(ebiten.KeyEnter) {
			g.leave()
		}
	}
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	switch g.scene {
	case sceneSelect:
		g.drawSelect(screen)
	case sceneConnecting:
		g.drawMessage(screen, "Connecting...", "")
	case sceneOffice:
		g.drawOffice(screen)
	case sceneError:
		g.drawMessage(screen, g.errTitle, g.err+"\n\nClick to go back")
	}
	drawCredit(screen) // 常に最前面・画面中央
}

// --- 性別選択 ---

func (g *Game) updateSelect() {
	g.pollProfileAsync()
	g.nameField.update()

	g.hoverCard = -1
	mx, my := ebiten.CursorPosition()
	for i, r := range g.cardRects {
		if image.Pt(mx, my).In(r) {
			g.hoverCard = i
		}
	}

	choice := -1
	if !g.nameField.focused { // 名前を入力しているときは、数字キーを文字として扱う
		switch {
		case inpututil.IsKeyJustPressed(ebiten.Key1):
			choice = 0
		case inpututil.IsKeyJustPressed(ebiten.Key2):
			choice = 1
		case inpututil.IsKeyJustPressed(ebiten.Key3):
			choice = 2
		}
	}
	if click, ok := justClicked(); ok {
		g.nameField.focused = click.In(g.fieldRect)
		if g.canPickIcon() && click.In(g.iconBtnRect) {
			g.pickIcon()
		}
		for i, r := range g.cardRects {
			if click.In(r) {
				choice = i
			}
		}
	}
	if choice >= 0 {
		g.enter(sim.Gender(choice))
	}
}

// enter は選んだ性別で入室する。オンラインなら接続を始め、オフラインならすぐにオフィスに入る。
func (g *Game) enter(gender sim.Gender) {
	g.w = netcode.NewWorld(g.m)
	g.frame, g.pendingClick, g.keys, g.lastDirX, g.lastDirY = 0, nil, [4]bool{}, 0, 0

	name := strings.TrimSpace(g.nameField.String())
	if name == "" {
		name = g.cfg.Name
	}
	iconPNG := g.iconPNG
	g.nameField.focused = false
	if !g.online() {
		if name == "" {
			name = "You"
		}
		g.w.JoinOffline(name, gender)
		g.scene = sceneOffice
		return
	}

	g.scene = sceneConnecting
	dial := make(chan dialResult, 1)
	g.dial = dial
	cfg := g.cfg
	go func() { // 接続の待ち時間でゲームループを止めない
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		url, token := cfg.ServerURL, cfg.RoomToken
		iconHash := ""
		if cfg.InviteCode != "" {
			j, p, err := joinViaAPI(ctx, cfg, name, gender, iconPNG)
			if err != nil {
				dial <- dialResult{err: err}
				return
			}
			url, token = j.ServerURL, j.RoomToken
			if p.IconHash != nil {
				iconHash = *p.IconHash
			}
		}
		c, err := netcode.Dial(ctx, url, &officev1.Hello{
			RoomToken: token,
			Profile:   &officev1.Profile{DisplayName: name, Gender: wire.GenderToProto(gender), IconHash: iconHash},
		})
		dial <- dialResult{c: c, err: err}
	}()
}

func (g *Game) updateConnecting() {
	select {
	case r := <-g.dial:
		if r.err != nil {
			g.fail(dialErrorText(r.err))
			return
		}
		g.conn = r.c
		g.scene = sceneOffice
	default:
	}
}

func (g *Game) fail(msg string) {
	if g.conn != nil {
		g.conn.Close()
		g.conn = nil
	}
	// 入室できなかったのか、入室後に切れたのかで、見出しを分ける
	g.errTitle = "Disconnected"
	if g.scene == sceneConnecting || (g.w != nil && !g.w.Ready) {
		g.errTitle = "Cannot join"
	}
	g.err, g.scene = msg, sceneError
}

// leave は接続を閉じて選択画面に戻る。
func (g *Game) leave() {
	if g.conn != nil {
		g.conn.Close()
		g.conn = nil
	}
	g.w, g.err, g.scene = nil, "", sceneSelect
}

func (g *Game) drawSelect(screen *ebiten.Image) {
	screen.Fill(color.RGBA{0x1b, 0x20, 0x30, 0xff})
	white := color.RGBA{0xff, 0xff, 0xff, 0xff}
	drawText(screen, "Gopher Office", 40, screenW/2, 40, alignCenter, white)
	drawText(screen, "Choose your Gopher", 18, screenW/2, 96, alignCenter, color.RGBA{0xb8, 0xc0, 0xd8, 0xff})

	for i, r := range g.cardRects {
		bg := color.RGBA{0x26, 0x2d, 0x44, 0xff}
		if i == g.hoverCard {
			bg = color.RGBA{0x34, 0x3e, 0x5e, 0xff}
		}
		vector.FillRect(screen, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), bg, false)
		vector.StrokeRect(screen, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), 2, bodyColor(sim.Gender(i)), false)

		op := &ebiten.DrawImageOptions{}
		op.GeoM.Scale(2.2, 2.2)
		op.GeoM.Translate(float64(r.Min.X)+(float64(r.Dx())-spriteW*2.2)/2, float64(r.Min.Y)+10)
		screen.DrawImage(g.sprites[i], op)

		label := genderLabel(sim.Gender(i)) + " [" + string(rune('1'+i)) + "]"
		drawText(screen, label, 18, float64(r.Min.X+r.Dx()/2), float64(r.Max.Y)-32, alignCenter, white)
	}

	g.drawProfilePanel(screen)

	hint := "Click a card or press 1 / 2 / 3"
	if !g.online() {
		hint += "  (offline)"
	}
	drawText(screen, hint, 16, screenW/2, 580, alignCenter, color.RGBA{0xb8, 0xc0, 0xd8, 0xff})
}

// drawMessage は接続中・エラーなどの案内を画面の上のほうに出す（中央はクレジットのため空ける）。
func (g *Game) drawMessage(screen *ebiten.Image, title, body string) {
	screen.Fill(color.RGBA{0x1b, 0x20, 0x30, 0xff})
	drawText(screen, title, 32, screenW/2, 90, alignCenter, color.RGBA{0xff, 0xff, 0xff, 0xff})
	if body != "" {
		drawText(screen, body, 16, screenW/2, 150, alignCenter, color.RGBA{0xb8, 0xc0, 0xd8, 0xff})
	}
}

// --- オフィス ---

func (g *Game) updateOffice() {
	if g.online() && !g.drainServer() {
		return
	}

	if !g.w.Ready {
		return // Welcome を待っている
	}

	// 入力は毎フレーム読み取り、tick の境界で反映する
	if click, ok := justClicked(); ok {
		tx, ty := click.X/tilePx, (click.Y-mapOffsetY)/tilePx
		if click.Y >= mapOffsetY && g.m.In(tx, ty) {
			p := sim.Point{X: tx, Y: ty}
			g.pendingClick = &p
		}
	}

	// tick の間に離された短いタップも取りこぼさないよう、押されたキーを tick が来るまで貯める
	for i, k := range moveKeys {
		if ebiten.IsKeyPressed(k) || inpututil.IsKeyJustPressed(k) {
			g.keys[i] = true
		}
	}

	g.frame++
	if g.frame < framesPerTick {
		return
	}
	g.frame = 0
	g.step()
}

// drainServer は届いているサーバのメッセージを世界に反映する。続行できなければ false。
func (g *Game) drainServer() bool {
	for {
		select {
		case m, ok := <-g.conn.Recv():
			if !ok {
				reason := "The connection was closed."
				if err := g.conn.Err(); err != nil && !errors.Is(err, netcode.ErrClosed) {
					reason = err.Error()
				}
				g.fail(reason)
				return false
			}
			if err := g.w.Apply(m); err != nil {
				g.fail(serverErrorText(err))
				return false
			}
		default:
			return true
		}
	}
}

func serverErrorText(err error) string {
	var se *netcode.ServerError
	if errors.As(err, &se) {
		switch se.Code {
		case officev1.ErrorCode_ERROR_CODE_FULL:
			return "The room is full."
		case officev1.ErrorCode_ERROR_CODE_BAD_TOKEN:
			return "The invitation is invalid or expired."
		case officev1.ErrorCode_ERROR_CODE_RATE_LIMITED:
			return "Too many requests."
		}
		return se.Message
	}
	return err.Error()
}

// moveKeys は g.keys の添字と対応する移動キー（左・右・上・下）。
var moveKeys = [4]ebiten.Key{ebiten.KeyA, ebiten.KeyD, ebiten.KeyW, ebiten.KeyS}

// step は 1 tick 進める。自分の入力は先に反映（予測）しつつ、変化があったときだけサーバに送る。
func (g *Game) step() {
	dx, dy := 0, 0
	if g.keys[0] {
		dx--
	}
	if g.keys[1] {
		dx++
	}
	if g.keys[2] {
		dy--
	}
	if g.keys[3] {
		dy++
	}
	g.keys = [4]bool{}

	switch {
	case dx != 0 || dy != 0:
		g.w.SetSelfDir(dx, dy) // 方向入力はクリック移動より優先
		g.pendingClick = nil
		if dx != g.lastDirX || dy != g.lastDirY {
			g.send(&officev1.ClientMessage{Body: &officev1.ClientMessage_MoveDir{MoveDir: &officev1.MoveDir{Dx: int32(dx), Dy: int32(dy)}}}) //nolint:gosec // -1〜1
		}
		g.lastDirX, g.lastDirY = dx, dy
	case g.pendingClick != nil:
		p := *g.pendingClick
		g.pendingClick = nil
		if g.w.MoveSelfTo(p) {
			g.send(&officev1.ClientMessage{Body: &officev1.ClientMessage_MoveTo{MoveTo: &officev1.MoveTo{X: int32(p.X), Y: int32(p.Y)}}}) //nolint:gosec // マップ内のタイル座標
		}
		g.lastDirX, g.lastDirY = 0, 0
	case g.lastDirX != 0 || g.lastDirY != 0: // キーを離した
		g.w.SetSelfDir(0, 0)
		g.send(&officev1.ClientMessage{Body: &officev1.ClientMessage_MoveDir{MoveDir: &officev1.MoveDir{}}})
		g.lastDirX, g.lastDirY = 0, 0
	}

	g.w.Step()
}

func (g *Game) send(m *officev1.ClientMessage) {
	if g.conn != nil {
		g.conn.Send(m)
	}
}

func (g *Game) drawOffice(screen *ebiten.Image) {
	screen.Fill(color.RGBA{0x14, 0x17, 0x22, 0xff})
	bgOp := &ebiten.DrawImageOptions{}
	bgOp.GeoM.Translate(0, mapOffsetY)
	screen.DrawImage(g.bg, bgOp)

	if !g.w.Ready {
		drawText(screen, "Joining...", 24, screenW/2, 60, alignCenter, color.RGBA{0xff, 0xff, 0xff, 0xff})
		return
	}
	self := g.w.Self()

	// クリック先のマーカー
	if t, ok := self.Actor.Target(); ok {
		cx := float32(t.X*tilePx + tilePx/2)
		cy := float32(t.Y*tilePx + tilePx/2 + mapOffsetY)
		vector.StrokeCircle(screen, cx, cy, 10, 2, color.RGBA{0xff, 0xff, 0xff, 0xb0}, true)
	}

	// 描画位置は直前の tick と現在の tick の間を線形補間する。奥（y が小さい）から手前の順に描く
	alpha := float64(g.frame) / framesPerTick
	g.items = g.items[:0]
	for _, p := range g.w.Players {
		x, y := p.RenderPos(alpha)
		g.items = append(g.items, drawable{x: x * subToPx, y: y*subToPx + mapOffsetY, p: p})
	}
	sort.Slice(g.items, func(i, j int) bool {
		if g.items[i].y != g.items[j].y {
			return g.items[i].y < g.items[j].y
		}
		return g.items[i].p.ID < g.items[j].p.ID
	})

	for _, it := range g.items {
		op := &ebiten.DrawImageOptions{}
		op.GeoM.Translate(it.x-spriteW/2, it.y-(spriteH-4)) // 足元が座標になるよう合わせる
		screen.DrawImage(g.sprites[it.p.Gender], op)

		iconImg, st := g.icons.get(it.p.ID, it.p.IconHash)
		tag := g.nameTag(it.p.Name, it.p.ID == g.w.SelfID, st, it.p.IconHash, iconImg)
		tw, th := tag.Bounds().Dx(), tag.Bounds().Dy()
		tagOp := &ebiten.DrawImageOptions{}
		tagOp.GeoM.Translate(it.x-float64(tw)/2, it.y-spriteH-2-float64(th))
		screen.DrawImage(tag, tagOp)
	}

	hud := "WASD / Click to move"
	if g.online() {
		hud = fmt.Sprintf("Online %d/%d   %d ms   ", len(g.w.Players), g.w.MaxPlayers, g.conn.RTT().Milliseconds()) + hud
	}
	drawText(screen, hud, 14, 12, 4, alignLeft, color.RGBA{0xff, 0xff, 0xff, 0xc0})
}

// justClicked はマウスの左クリックまたはタッチが始まった位置を返す。
func justClicked() (image.Point, bool) {
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		x, y := ebiten.CursorPosition()
		return image.Pt(x, y), true
	}
	if ids := inpututil.AppendJustPressedTouchIDs(nil); len(ids) > 0 {
		x, y := ebiten.TouchPosition(ids[0])
		return image.Pt(x, y), true
	}
	return image.Point{}, false
}
