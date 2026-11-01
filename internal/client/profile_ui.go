package client

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/o-ga09/gopher-office/internal/apiclient"
)

// プロフィール編集（選択画面の下側）の配置。画面中央にはクレジットが出るので、その下に置く。
const (
	profilePanelX = 360
	profilePanelY = 430
	previewSize   = 64
)

type pickResult struct {
	png []byte
	err error
}

// layoutProfilePanel は、名前の入力欄とアイコンのボタンの位置を決める。
func (g *Game) layoutProfilePanel() {
	y := profilePanelY + 22
	g.fieldRect = image.Rect(profilePanelX+previewSize+20, y, profilePanelX+previewSize+20+296, y+36)
	g.iconBtnRect = image.Rect(g.fieldRect.Max.X+16, y, g.fieldRect.Max.X+16+164, y+36)
	by := g.fieldRect.Max.Y + 16
	g.workspacesBtnRect = image.Rect(g.fieldRect.Min.X, by, g.fieldRect.Min.X+170, by+32)
	g.signInBtnRect = image.Rect(g.workspacesBtnRect.Max.X+16, by, g.workspacesBtnRect.Max.X+16+120, by+32)
}

// canPickIcon は、アイコンを設定できるかを返す。API（ゲストの登録）を通す入室で、かつブラウザ版のときだけ。
func (g *Game) canPickIcon() bool { return g.inviteCode != "" && g.cfg.IconPicker != nil }

// pickIcon は画像の選択ダイアログを開く。
func (g *Game) pickIcon() {
	g.pickErr = ""
	pickCh := g.pickCh
	g.cfg.IconPicker.Pick(func(pngData []byte, err error) {
		select {
		case pickCh <- pickResult{png: pngData, err: err}:
		default: // 前の結果がまだ取り込まれていなければ捨てる
		}
	})
}

// loadPreviousProfile は、前回のトークンが残っていれば、前回の名前とアイコンを取ってくる。
func (g *Game) loadPreviousProfile() {
	if g.inviteCode == "" || g.cfg.Store == nil {
		return
	}
	tok := g.cfg.Store.Load()
	if tok == "" {
		return
	}
	base, ch := g.cfg.APIBase, g.profileCh
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		api := apiclient.New(base)
		api.SetToken(tok)
		if p, err := api.Me(ctx); err == nil { // 失敗しても、新しいゲストとして入れるので何もしない
			ch <- p
		}
	}()
}

// pollProfileAsync は、バックグラウンドで終わった処理（画像の選択・前回のプロフィールの取得）を取り込む。
func (g *Game) pollProfileAsync() {
	select {
	case r := <-g.pickCh:
		g.applyPick(r)
	default:
	}
	select {
	case p := <-g.profileCh:
		g.prevProfile = &p
		if g.nameField.String() == "" && !g.nameField.focused {
			g.nameField.set(p.DisplayName)
		}
	default:
	}
}

func (g *Game) applyPick(r pickResult) {
	if r.err != nil {
		g.pickErr = "Could not use that image. Try a PNG, JPEG, or WebP file."
		return
	}
	img, _, err := image.Decode(bytes.NewReader(r.png))
	if err != nil {
		g.pickErr = "Could not use that image. Try a PNG, JPEG, or WebP file."
		return
	}
	if g.iconPreview != nil {
		g.iconPreview.Deallocate()
	}
	g.iconPNG, g.iconPreview, g.pickErr = r.png, ebiten.NewImageFromImage(img), ""
}

func (g *Game) drawProfilePanel(screen *ebiten.Image) {
	muted := color.RGBA{0xb8, 0xc0, 0xd8, 0xff}
	white := color.RGBA{0xff, 0xff, 0xff, 0xff}

	// アイコンのプレビュー: 選んだ画像 → 前回のアイコン → シルエット
	prev := image.Rect(profilePanelX, g.fieldRect.Min.Y-10, profilePanelX+previewSize, g.fieldRect.Min.Y-10+previewSize)
	img := g.iconPreview
	if img == nil && g.prevProfile != nil && g.prevProfile.IconHash != nil {
		img, _ = g.icons.get(g.prevProfile.ID, *g.prevProfile.IconHash)
	}
	if img != nil {
		op := &ebiten.DrawImageOptions{Filter: ebiten.FilterLinear}
		op.GeoM.Scale(previewSize/float64(img.Bounds().Dx()), previewSize/float64(img.Bounds().Dy()))
		op.GeoM.Translate(float64(prev.Min.X), float64(prev.Min.Y))
		screen.DrawImage(img, op)
	} else {
		drawSilhouette(screen, float32(prev.Min.X), float32(prev.Min.Y), previewSize)
	}
	vector.StrokeRect(screen, float32(prev.Min.X), float32(prev.Min.Y), previewSize, previewSize, 1, color.RGBA{0xff, 0xff, 0xff, 0x50}, false)

	// 名前の入力欄
	drawText(screen, "Name", 14, float64(g.fieldRect.Min.X), float64(g.fieldRect.Min.Y-22), alignLeft, muted)
	r := g.fieldRect
	vector.FillRect(screen, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), color.RGBA{0x10, 0x14, 0x20, 0xff}, false)
	border := color.RGBA{0xff, 0xff, 0xff, 0x50}
	if g.nameField.focused {
		border = color.RGBA{0x4a, 0x90, 0xd9, 0xff}
	}
	vector.StrokeRect(screen, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), 2, border, false)
	switch s := g.nameField.String(); {
	case s == "" && !g.nameField.focused:
		drawText(screen, "Click to enter your name", 16, float64(r.Min.X+10), float64(r.Min.Y+8), alignLeft, color.RGBA{0x80, 0x88, 0xa0, 0xff})
	default:
		if g.nameField.caretVisible() {
			s += "|"
		}
		drawText(screen, s, 16, float64(r.Min.X+10), float64(r.Min.Y+8), alignLeft, white)
	}

	// アイコンのボタン
	switch {
	case g.canPickIcon():
		b := g.iconBtnRect
		bg := color.RGBA{0x26, 0x2d, 0x44, 0xff}
		if mx, my := ebiten.CursorPosition(); image.Pt(mx, my).In(b) {
			bg = color.RGBA{0x34, 0x3e, 0x5e, 0xff}
		}
		vector.FillRect(screen, float32(b.Min.X), float32(b.Min.Y), float32(b.Dx()), float32(b.Dy()), bg, false)
		vector.StrokeRect(screen, float32(b.Min.X), float32(b.Min.Y), float32(b.Dx()), float32(b.Dy()), 1, color.RGBA{0xff, 0xff, 0xff, 0x80}, false)
		drawText(screen, "Choose icon...", 16, float64(b.Min.X+b.Dx()/2), float64(b.Min.Y+8), alignCenter, white)
	case g.inviteCode != "":
		drawText(screen, "Icons can be set in the\nbrowser version", 13, float64(g.iconBtnRect.Min.X), float64(g.iconBtnRect.Min.Y), alignLeft, muted)
	}
	g.drawProfileButtons(screen)
	if g.pickErr != "" {
		drawText(screen, g.pickErr, 14, float64(g.fieldRect.Min.X), float64(g.fieldRect.Max.Y+10), alignLeft, color.RGBA{0xff, 0x8a, 0x8a, 0xff})
	}
}

// clickProfileButtons は、プロフィール画面の「My workspaces」「Sign in」のクリックを扱う。ホーム画面のときだけ有効。
func (g *Game) clickProfileButtons(click image.Point) {
	if !g.cfg.Home {
		return
	}
	switch {
	case click.In(g.workspacesBtnRect):
		g.scene = sceneLobby
	case click.In(g.signInBtnRect):
		g.scene = sceneAccount
	}
}

func (g *Game) drawProfileButtons(screen *ebiten.Image) {
	if !g.cfg.Home {
		return
	}
	mx, my := ebiten.CursorPosition()
	for _, b := range []struct {
		r     image.Rectangle
		label string
	}{{g.workspacesBtnRect, "My workspaces"}, {g.signInBtnRect, "Sign in"}} {
		bg := color.RGBA{0x26, 0x2d, 0x44, 0xff}
		if image.Pt(mx, my).In(b.r) {
			bg = color.RGBA{0x34, 0x3e, 0x5e, 0xff}
		}
		vector.FillRect(screen, float32(b.r.Min.X), float32(b.r.Min.Y), float32(b.r.Dx()), float32(b.r.Dy()), bg, false)
		vector.StrokeRect(screen, float32(b.r.Min.X), float32(b.r.Min.Y), float32(b.r.Dx()), float32(b.r.Dy()), 1, color.RGBA{0xff, 0xff, 0xff, 0x80}, false)
		drawText(screen, b.label, 15, float64(b.r.Min.X+b.r.Dx()/2), float64(b.r.Min.Y+7), alignCenter, color.RGBA{0xff, 0xff, 0xff, 0xff})
	}
}
