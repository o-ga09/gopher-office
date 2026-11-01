package client

import (
	"context"
	"image"
	"image/color"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/o-ga09/gopher-office/internal/apiclient"
)

const lobbyRowH = 40

var (
	lobbyList    = image.Rect(380, 150, 900, 150+8*lobbyRowH)
	lobbyNameBox = image.Rect(380, 490, 700, 526)
	lobbyNewBtn  = image.Rect(712, 490, 900, 526)
	lobbyBackBtn = image.Rect(380, 540, 500, 572)
)

// lobbyUI はワークスペース一覧（My workspaces）の状態。
type lobbyUI struct {
	ch      chan func()
	started bool
	loading bool
	list    []apiclient.MyWorkspace
	err     string
	name    textField
}

func (l *lobbyUI) poll() {
	for {
		select {
		case f := <-l.ch:
			f()
		default:
			return
		}
	}
}

func (g *Game) loadLobby() {
	l := &g.lobby
	if l.ch == nil {
		l.ch = make(chan func(), 4)
		l.name = textField{max: 40}
		l.name.set("My workspace")
	}
	l.started, l.loading, l.err = true, true, ""
	if g.cfg.Store == nil || g.cfg.Store.Load() == "" {
		l.loading = false
		return
	}
	api := g.authAPI()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ws, err := api.MyWorkspaces(ctx)
		l.ch <- func() {
			l.loading = false
			if err != nil {
				l.err = dialErrorText(err)
				return
			}
			l.list = sortWorkspaces(ws)
		}
	}()
}

func (g *Game) createWorkspace() {
	l := &g.lobby
	name := l.name.String()
	if name == "" {
		return
	}
	l.loading, l.err = true, ""
	api := g.authAPI()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := api.CreateWorkspace(ctx, name)
		l.ch <- func() {
			if err != nil {
				l.loading, l.err = false, "Could not create the workspace.\nSign in or join a workspace first."
				return
			}
			g.loadLobby()
		}
	}()
}

// openWorkspace は選んだワークスペースの URL に書き換えて、入室前の画面（プロフィール）に進む。
func (g *Game) openWorkspace(code string) {
	g.inviteCode = code
	g.rooms = roomsUI{}
	if g.cfg.Navigator != nil {
		g.cfg.Navigator.Push(workspacePath(code))
	}
	g.lobby.started = false
	g.loadPreviousProfile()
	g.scene = sceneSelect
}

// updateLobby はワークスペース一覧（My workspaces）の更新。
func (g *Game) updateLobby() {
	l := &g.lobby
	if !l.started {
		g.loadLobby()
	}
	l.poll()
	l.name.update()
	if !l.name.focused && inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		l.started = false
		g.scene = sceneSelect
		return
	}
	click, ok := justClicked()
	if !ok {
		return
	}
	l.name.focused = click.In(lobbyNameBox)
	switch {
	case click.In(lobbyBackBtn):
		l.started = false
		g.scene = sceneSelect
	case click.In(lobbyNewBtn) && !l.loading:
		g.createWorkspace()
	default:
		if i := rowAt(click, lobbyList, lobbyRowH, min(len(l.list), 8)); i >= 0 {
			g.openWorkspace(l.list[i].InviteCode)
		}
	}
}

// drawLobby はワークスペース一覧の描画。
func (g *Game) drawLobby(screen *ebiten.Image) {
	l := &g.lobby
	screen.Fill(color.RGBA{0x1b, 0x20, 0x30, 0xff})
	white := color.RGBA{0xff, 0xff, 0xff, 0xff}
	muted := color.RGBA{0xb8, 0xc0, 0xd8, 0xff}
	drawText(screen, "My workspaces", 32, screenW/2, 70, alignCenter, white)
	switch {
	case l.err != "":
		drawText(screen, l.err, 14, float64(lobbyList.Min.X), 120, alignLeft, color.RGBA{0xff, 0x8a, 0x8a, 0xff})
	case l.loading:
		drawText(screen, "Loading...", 16, float64(lobbyList.Min.X), 120, alignLeft, muted)
	case len(l.list) == 0:
		drawText(screen, "No workspaces yet", 16, float64(lobbyList.Min.X), 120, alignLeft, muted)
	}
	mx, my := ebiten.CursorPosition()
	for i, w := range l.list {
		if i >= 8 {
			break
		}
		row := image.Rect(lobbyList.Min.X, lobbyList.Min.Y+i*lobbyRowH, lobbyList.Max.X, lobbyList.Min.Y+(i+1)*lobbyRowH-4)
		bg := color.RGBA{0x26, 0x2d, 0x44, 0xff}
		if image.Pt(mx, my).In(row) {
			bg = color.RGBA{0x34, 0x3e, 0x5e, 0xff}
		}
		vector.FillRect(screen, float32(row.Min.X), float32(row.Min.Y), float32(row.Dx()), float32(row.Dy()), bg, false)
		drawText(screen, w.Name, 16, float64(row.Min.X+12), float64(row.Min.Y+8), alignLeft, white)
		drawText(screen, w.Role, 13, float64(row.Max.X-12-60), float64(row.Min.Y+10), alignLeft, muted)
	}

	r := lobbyNameBox
	vector.FillRect(screen, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), color.RGBA{0x10, 0x14, 0x20, 0xff}, false)
	border := color.RGBA{0xff, 0xff, 0xff, 0x50}
	if l.name.focused {
		border = color.RGBA{0x4a, 0x90, 0xd9, 0xff}
	}
	vector.StrokeRect(screen, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), 2, border, false)
	s := l.name.String()
	if l.name.caretVisible() {
		s += "|"
	}
	drawText(screen, s, 16, float64(r.Min.X+10), float64(r.Min.Y+8), alignLeft, white)
	drawListButton(screen, lobbyNewBtn, "+ New workspace", 15)
	drawListButton(screen, lobbyBackBtn, "Back", 15)
}
