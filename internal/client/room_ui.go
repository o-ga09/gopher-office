package client

import (
	"context"
	"image"
	"image/color"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"

	officev1 "github.com/o-ga09/gopher-office/gen/office/v1"
	"github.com/o-ga09/gopher-office/internal/apiclient"
	"github.com/o-ga09/gopher-office/internal/netcode"
	"github.com/o-ga09/gopher-office/internal/wire"
)

const roomRowH = 36

var (
	roomPanel   = image.Rect(380, 110, 900, 470)
	roomList    = image.Rect(400, 170, 880, 170+8*roomRowH)
	roomAddBtn  = image.Rect(400, 430, 520, 462)
	roomClose   = image.Rect(780, 430, 880, 462)
	roomChooser = image.Rect(profilePanelX+previewSize+20, 546, profilePanelX+previewSize+20+296, 572)
)

// roomsUI はルームの一覧（入室後のオーバーレイ）と、プロフィール画面での選択の状態。
// 通信の結果は ch に積んだ関数で、ゲームループ上で反映する。
type roomsUI struct {
	ch chan func()

	// 入室後のオーバーレイ
	open    bool
	loading bool
	rooms   []apiclient.Room
	owner   bool
	err     string
	current string // 今いるルームの ID

	// プロフィール画面での選択
	pickCode  string // pickRooms を取得した招待コード
	pickRooms []apiclient.Room
	pickID    string // 選んだルームの ID（空なら API に任せる）
}

func (u *roomsUI) init() {
	if u.ch == nil {
		u.ch = make(chan func(), 8)
	}
}

func (u *roomsUI) poll() {
	u.init()
	for {
		select {
		case f := <-u.ch:
			f()
		default:
			return
		}
	}
}

// authAPI は保存済みの playerToken を持つ API クライアント。
func (g *Game) authAPI() *apiclient.Client {
	api := apiclient.New(g.cfg.APIBase)
	if g.cfg.Store != nil {
		api.SetToken(g.cfg.Store.Load())
	}
	return api
}

// --- プロフィール画面でのルーム選択 ---

// updateRoomPick は、ルームが 2 つ以上あるワークスペースのとき、選択肢を出す。
func (g *Game) updateRoomPick() {
	u := &g.rooms
	u.poll()
	code := g.inviteCode
	if code == "" || g.cfg.APIBase == "" {
		return
	}
	if u.pickCode != code {
		u.pickCode, u.pickRooms, u.pickID = code, nil, ""
		api := apiclient.New(g.cfg.APIBase)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			rooms, err := api.Rooms(ctx, code)
			if err != nil {
				return
			}
			u.ch <- func() {
				if u.pickCode == code {
					u.pickRooms = rooms
				}
			}
		}()
	}
	if click, ok := justClicked(); ok && len(u.pickRooms) >= 2 && click.In(roomChooser) {
		u.pickID = nextRoomID(u.pickRooms, u.pickID)
	}
}

func (g *Game) drawRoomPick(screen *ebiten.Image) {
	u := &g.rooms
	if g.inviteCode == "" || u.pickCode != g.inviteCode || len(u.pickRooms) < 2 {
		return
	}
	name := roomName(u.pickRooms, u.pickID)
	if name == "" {
		name = "Auto"
	}
	drawListButton(screen, roomChooser, "Room: "+name+"  >", 14)
}

// --- 入室後のルーム一覧 ---

// roomsAvailable は、API 経由で入室していて、ルームを切り替えられるか。
func (g *Game) roomsAvailable() bool {
	return g.inviteCode != "" && g.conn != nil && g.cfg.APIBase != ""
}

// updateRooms は R キーのオーバーレイを扱う。オーバーレイが開いていて入力を使ったら true。
func (g *Game) updateRooms() bool {
	u := &g.rooms
	u.poll()
	if !g.roomsAvailable() {
		return false
	}
	if !u.open {
		if inpututil.IsKeyJustPressed(ebiten.KeyR) {
			g.openRooms()
			return true
		}
		return false
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) || inpututil.IsKeyJustPressed(ebiten.KeyR) {
		u.open = false
		return true
	}
	click, ok := justClicked()
	if !ok {
		return true
	}
	switch {
	case click.In(roomClose) || !click.In(roomPanel):
		u.open = false
	case u.owner && click.In(roomAddBtn):
		g.createRoom()
	default:
		if i := rowAt(click, roomList, roomRowH, len(u.rooms)); i >= 0 {
			if id, ok := switchTarget(u.current, u.rooms[i].ID); ok {
				u.open = false
				g.switchRoom(id)
			}
		}
	}
	return true
}

func (g *Game) openRooms() {
	u := &g.rooms
	u.init()
	u.open, u.loading, u.err = true, true, ""
	code, api := g.inviteCode, g.authAPI()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rooms, err := api.Rooms(ctx, code)
		owner := false
		if err == nil {
			if ws, e := api.MyWorkspaces(ctx); e == nil {
				owner = isOwner(ws, code)
			}
		}
		u.ch <- func() {
			u.loading = false
			if err != nil {
				u.err = dialErrorText(err)
				return
			}
			u.rooms, u.owner = rooms, owner
		}
	}()
}

func (g *Game) createRoom() {
	u := &g.rooms
	u.loading, u.err = true, ""
	code, api, name := g.inviteCode, g.authAPI(), nextRoomName(len(u.rooms))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		room, err := api.CreateRoom(ctx, code, name)
		u.ch <- func() {
			u.loading = false
			if err != nil {
				u.err = "Could not create the room."
				return
			}
			u.rooms = append(u.rooms, room)
		}
	}()
}

// switchRoom は接続を閉じて、指定したルームに入り直す。
func (g *Game) switchRoom(roomID string) {
	self := g.w.Self()
	name, gender := self.Name, self.Gender
	g.closeVoice()
	if g.conn != nil {
		g.conn.Close()
		g.conn = nil
	}
	g.w = netcode.NewWorld(g.m)
	g.frame, g.pendingClick, g.keys, g.lastDirX, g.lastDirY = 0, nil, [4]bool{}, 0, 0
	g.scene = sceneConnecting
	dial := make(chan dialResult, 1)
	g.dial = dial
	cfg, code := g.cfg, g.inviteCode
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		s := newSession(cfg, name, gender, nil)
		_, me, err := s.ensureSession(ctx)
		if err != nil {
			dial <- dialResult{err: err}
			return
		}
		j, err := s.join(ctx, code, roomID)
		if err != nil {
			dial <- dialResult{err: err}
			return
		}
		iconHash := ""
		if me.IconHash != nil {
			iconHash = *me.IconHash
		}
		c, err := netcode.Dial(ctx, j.DialURL(), &officev1.Hello{
			RoomToken: j.RoomToken,
			Profile:   &officev1.Profile{DisplayName: name, Gender: wire.GenderToProto(gender), IconHash: iconHash},
		})
		dial <- dialResult{c: c, err: err, roomID: j.Room.ID}
	}()
}

func (g *Game) drawRooms(screen *ebiten.Image) {
	u := &g.rooms
	if !g.roomsAvailable() {
		return
	}
	white := color.RGBA{0xff, 0xff, 0xff, 0xff}
	muted := color.RGBA{0xb8, 0xc0, 0xd8, 0xff}
	if !u.open {
		drawText(screen, "R: rooms", 14, screenW-90, 4, alignLeft, muted)
		return
	}
	p := roomPanel
	vector.FillRect(screen, float32(p.Min.X), float32(p.Min.Y), float32(p.Dx()), float32(p.Dy()), color.RGBA{0x1b, 0x20, 0x30, 0xf0}, false)
	vector.StrokeRect(screen, float32(p.Min.X), float32(p.Min.Y), float32(p.Dx()), float32(p.Dy()), 2, color.RGBA{0xff, 0xff, 0xff, 0x80}, false)
	drawText(screen, "Rooms", 26, float64(p.Min.X+20), float64(p.Min.Y+16), alignLeft, white)
	switch {
	case u.err != "":
		drawText(screen, u.err, 14, float64(p.Min.X+20), float64(p.Min.Y+64), alignLeft, color.RGBA{0xff, 0x8a, 0x8a, 0xff})
	case u.loading && len(u.rooms) == 0:
		drawText(screen, "Loading...", 16, float64(p.Min.X+20), float64(p.Min.Y+64), alignLeft, muted)
	}
	mx, my := ebiten.CursorPosition()
	for i, r := range roomRows(u.rooms, u.current) {
		if i >= 8 {
			break
		}
		row := image.Rect(roomList.Min.X, roomList.Min.Y+i*roomRowH, roomList.Max.X, roomList.Min.Y+(i+1)*roomRowH-4)
		bg := color.RGBA{0x26, 0x2d, 0x44, 0xff}
		if image.Pt(mx, my).In(row) {
			bg = color.RGBA{0x34, 0x3e, 0x5e, 0xff}
		}
		vector.FillRect(screen, float32(row.Min.X), float32(row.Min.Y), float32(row.Dx()), float32(row.Dy()), bg, false)
		label := r.Name
		if r.Current {
			label += "  (here)"
		}
		drawText(screen, label, 16, float64(row.Min.X+12), float64(row.Min.Y+6), alignLeft, white)
	}
	if u.owner {
		drawListButton(screen, roomAddBtn, "+ Room", 15)
	}
	drawListButton(screen, roomClose, "Close", 15)
}

// drawListButton は枠付きのボタンを描く。
func drawListButton(screen *ebiten.Image, r image.Rectangle, label string, size float64) {
	bg := color.RGBA{0x26, 0x2d, 0x44, 0xff}
	if mx, my := ebiten.CursorPosition(); image.Pt(mx, my).In(r) {
		bg = color.RGBA{0x34, 0x3e, 0x5e, 0xff}
	}
	vector.FillRect(screen, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), bg, false)
	vector.StrokeRect(screen, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), 1, color.RGBA{0xff, 0xff, 0xff, 0x80}, false)
	drawText(screen, label, size, float64(r.Min.X+r.Dx()/2), float64(r.Min.Y+(r.Dy()-int(size))/2-2), alignCenter, color.RGBA{0xff, 0xff, 0xff, 0xff})
}
