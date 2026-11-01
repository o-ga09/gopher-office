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

// 画面の文言は英語のみ（同梱フォントが日本語を持たないため）。日本語の文言は accountMessage に用意してある。

// accountOp は非同期に実行するアカウント操作の種類。
type accountOp int

const (
	opLoad     accountOp = iota // 画面を開いたときの、現在のアカウント情報の取得
	opRegister                  // ゲスト → アカウントに昇格
	opLogin
	opChangePassword
)

// accountResult は非同期の操作の結果。成功なら token は新しい playerToken（Me のときは空）。
type accountResult struct {
	op     accountOp
	player apiclient.Player
	token  string
	err    error
}

// accountUI はサインイン画面の状態。
type accountUI struct {
	creating bool // ゲスト時: true なら Create account、false なら Sign in
	email    textField
	pw       textField // ゲスト時: パスワード / ログイン済み時: 現在のパスワード
	newPw    textField // ログイン済み時: 新しいパスワード
	me       *apiclient.Player
	busy     bool
	msg      string
	ok       bool // msg が成功の知らせか
	ch       chan accountResult
}

func newAccountUI() *accountUI {
	return &accountUI{
		creating: true,
		email:    textField{max: maxEmailLen},
		pw:       textField{max: maxPasswordLen, maxByte: maxPasswordLen, mask: true},
		newPw:    textField{max: maxPasswordLen, maxByte: maxPasswordLen, mask: true},
		ch:       make(chan accountResult, 1),
	}
}

// 画面のレイアウト
var (
	acctTabCreate = image.Rect(460, 170, 640, 210)
	acctTabSignIn = image.Rect(640, 170, 820, 210)
	acctField1    = image.Rect(460, 260, 820, 296)
	acctField2    = image.Rect(460, 336, 820, 372)
	acctSubmit    = image.Rect(460, 400, 820, 440)
	acctSignOut   = image.Rect(460, 456, 820, 496)
	acctBack      = image.Rect(460, 600, 820, 640)
)

func (a *accountUI) fields() []*textField {
	if a.me != nil {
		return []*textField{&a.pw, &a.newPw}
	}
	return []*textField{&a.email, &a.pw}
}

func (a *accountUI) focus(f *textField) {
	for _, x := range []*textField{&a.email, &a.pw, &a.newPw} {
		x.focused = x == f
	}
}

func (a *accountUI) anyFocused() bool { return a.email.focused || a.pw.focused || a.newPw.focused }

// updateAccount はサインイン画面の更新。
func (g *Game) updateAccount() {
	if g.acct == nil {
		g.acct = newAccountUI()
		g.startAccountOp(opLoad, "", "")
	}
	a := g.acct
	g.pollAccount()

	// Escape はフォーカスを外すだけの場合があるので、update の前に判定する
	escToLeave := inpututil.IsKeyJustPressed(ebiten.KeyEscape) && !a.anyFocused()
	enter := inpututil.IsKeyJustPressed(ebiten.KeyEnter) && a.anyFocused()
	for _, f := range []*textField{&a.email, &a.pw, &a.newPw} {
		f.update()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyTab) {
		fs := a.fields()
		for i, f := range fs {
			if f.focused {
				a.focus(fs[(i+1)%len(fs)])
				break
			}
		}
	}
	if escToLeave {
		g.leaveAccount()
		return
	}
	if enter {
		g.submitAccount()
	}

	click, ok := justClicked()
	if !ok {
		return
	}
	switch {
	case click.In(acctBack):
		g.leaveAccount()
	case a.me != nil:
		g.clickSignedIn(click)
	default:
		g.clickGuest(click)
	}
}

func (g *Game) leaveAccount() {
	g.acct = nil
	g.scene = sceneSelect
}

func (g *Game) clickGuest(click image.Point) {
	a := g.acct
	switch {
	case click.In(acctTabCreate):
		a.creating, a.msg = true, ""
	case click.In(acctTabSignIn):
		a.creating, a.msg = false, ""
	case click.In(acctField1):
		a.focus(&a.email)
	case click.In(acctField2):
		a.focus(&a.pw)
	case click.In(acctSubmit):
		g.submitAccount()
	default:
		a.focus(nil)
	}
}

func (g *Game) clickSignedIn(click image.Point) {
	a := g.acct
	switch {
	case click.In(acctField1):
		a.focus(&a.pw)
	case click.In(acctField2):
		a.focus(&a.newPw)
	case click.In(acctSubmit):
		g.submitAccount()
	case click.In(acctSignOut):
		g.signOut()
	default:
		a.focus(nil)
	}
}

// submitAccount は入力を検査して、操作を始める。
func (g *Game) submitAccount() {
	a := g.acct
	if a.busy {
		return
	}
	fail := func(code string) { a.msg, a.ok = accountMessage(code, langEN), false }
	if a.me != nil {
		if code := validatePassword(a.newPw.String()); code != "" {
			fail(code)
			return
		}
		g.startAccountOp(opChangePassword, a.pw.String(), a.newPw.String())
		return
	}
	email := normalizeEmail(a.email.String())
	if code := validateEmail(email); code != "" {
		fail(code)
		return
	}
	if a.creating {
		if code := validatePassword(a.pw.String()); code != "" {
			fail(code)
			return
		}
		g.startAccountOp(opRegister, email, a.pw.String())
		return
	}
	// サインインでは長さの検査をサーバに任せる
	g.startAccountOp(opLogin, email, a.pw.String())
}

// signOut は保存した playerToken を捨てる。次に入室するときは、新しいゲストになる。
func (g *Game) signOut() {
	if g.cfg.Store != nil {
		g.cfg.Store.Save("")
	}
	g.prevProfile = nil
	a := newAccountUI()
	a.msg, a.ok = "Signed out.", true
	g.acct = a
}

// startAccountOp はバックグラウンドで API を呼ぶ。結果は pollAccount が取り込む。
// opRegister / opLogin では（メール, パスワード）、opChangePassword では（現在の, 新しい）パスワード。
func (g *Game) startAccountOp(op accountOp, x, y string) {
	a := g.acct
	var tok string
	if g.cfg.Store != nil {
		tok = g.cfg.Store.Load()
	}
	if op == opLoad && (tok == "" || g.cfg.APIBase == "") {
		return // 保存したトークンがなければゲスト
	}
	if g.cfg.APIBase == "" {
		a.msg, a.ok = accountMessage("unavailable", langEN), false
		return
	}
	a.busy = true
	base, ch := g.cfg.APIBase, a.ch
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		api := apiclient.New(base)
		api.SetToken(tok)
		r := accountResult{op: op}
		switch op {
		case opLoad:
			r.player, r.err = api.Me(ctx)
		case opRegister:
			r.player, r.err = api.RegisterCredentials(ctx, x, y)
		case opLogin:
			r.player, r.err = api.Login(ctx, x, y)
		case opChangePassword:
			r.err = api.ChangePassword(ctx, x, y)
		}
		if r.err == nil && op != opLoad {
			r.token = api.Token()
		}
		ch <- r
	}()
}

// pollAccount は、終わった操作の結果を取り込む。新しい playerToken はここで保存する。
func (g *Game) pollAccount() {
	a := g.acct
	var r accountResult
	select {
	case r = <-a.ch:
	default:
		return
	}
	a.busy = false
	if r.err != nil {
		if r.op != opLoad {
			a.msg, a.ok = accountErrorMessage(r.err, langEN), false
		}
		return
	}
	if r.token != "" && g.cfg.Store != nil {
		g.cfg.Store.Save(r.token)
	}
	switch r.op {
	case opLoad:
		if r.player.Account != nil {
			p := r.player
			a.me = &p
		}
	case opRegister, opLogin:
		p := r.player
		g.prevProfile = &p
		a.me = nil
		if p.Account != nil {
			a.me = &p
		}
		a.pw.clear()
		a.focus(nil)
		a.msg, a.ok = "Signed in.", true
		if r.op == opRegister {
			a.msg = "Account created."
		}
	case opChangePassword:
		a.pw.clear()
		a.newPw.clear()
		a.focus(nil)
		a.msg, a.ok = "Password changed.", true
	}
}

// drawAccount はサインイン画面の描画。
func (g *Game) drawAccount(screen *ebiten.Image) {
	screen.Fill(color.RGBA{0x1b, 0x20, 0x30, 0xff})
	a := g.acct
	if a == nil {
		return
	}
	muted := color.RGBA{0xb8, 0xc0, 0xd8, 0xff}
	drawText(screen, "Account", 32, screenW/2, 90, alignCenter, color.RGBA{0xff, 0xff, 0xff, 0xff})

	if a.me != nil {
		drawText(screen, "Signed in as "+a.me.Account.Email, 16, screenW/2, 200, alignCenter, muted)
		drawField(screen, acctField1, "Current password", "", &a.pw)
		drawField(screen, acctField2, "New password (8+ characters)", "", &a.newPw)
		drawButton(screen, acctSubmit, "Change password", a.busy)
		drawButton(screen, acctSignOut, "Sign out", false)
	} else {
		drawTab(screen, acctTabCreate, "Create account", a.creating)
		drawTab(screen, acctTabSignIn, "Sign in", !a.creating)
		hint, label := "Sign in with an existing account.", "Sign in"
		if a.creating {
			hint, label = "Keep your profile: add an email and password.", "Create account"
		}
		drawText(screen, hint, 14, screenW/2, 222, alignCenter, muted)
		drawField(screen, acctField1, "Email", "you@example.com", &a.email)
		drawField(screen, acctField2, "Password", "", &a.pw)
		drawButton(screen, acctSubmit, label, a.busy)
	}
	if a.msg != "" {
		c := color.RGBA{0xff, 0x8a, 0x8a, 0xff}
		if a.ok {
			c = color.RGBA{0x8a, 0xe6, 0x9a, 0xff}
		}
		drawText(screen, a.msg, 15, screenW/2, 516, alignCenter, c)
	}
	drawButton(screen, acctBack, "Back", false)
}

func drawField(screen *ebiten.Image, r image.Rectangle, label, placeholder string, f *textField) {
	drawText(screen, label, 14, float64(r.Min.X), float64(r.Min.Y-22), alignLeft, color.RGBA{0xb8, 0xc0, 0xd8, 0xff})
	vector.FillRect(screen, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), color.RGBA{0x10, 0x14, 0x20, 0xff}, false)
	border := color.RGBA{0xff, 0xff, 0xff, 0x50}
	if f.focused {
		border = color.RGBA{0x4a, 0x90, 0xd9, 0xff}
	}
	vector.StrokeRect(screen, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), 2, border, false)
	s := f.display()
	if s == "" && !f.focused {
		drawText(screen, placeholder, 16, float64(r.Min.X+10), float64(r.Min.Y+8), alignLeft, color.RGBA{0x80, 0x88, 0xa0, 0xff})
		return
	}
	if f.caretVisible() {
		s += "|"
	}
	drawText(screen, s, 16, float64(r.Min.X+10), float64(r.Min.Y+8), alignLeft, color.RGBA{0xff, 0xff, 0xff, 0xff})
}

func drawButton(screen *ebiten.Image, r image.Rectangle, label string, busy bool) {
	bg := color.RGBA{0x26, 0x2d, 0x44, 0xff}
	if mx, my := ebiten.CursorPosition(); image.Pt(mx, my).In(r) && !busy {
		bg = color.RGBA{0x34, 0x3e, 0x5e, 0xff}
	}
	if busy {
		label += "..."
	}
	vector.FillRect(screen, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), bg, false)
	vector.StrokeRect(screen, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), 1, color.RGBA{0xff, 0xff, 0xff, 0x80}, false)
	drawText(screen, label, 16, float64(r.Min.X+r.Dx()/2), float64(r.Min.Y+10), alignCenter, color.RGBA{0xff, 0xff, 0xff, 0xff})
}

func drawTab(screen *ebiten.Image, r image.Rectangle, label string, active bool) {
	bg := color.RGBA{0x1f, 0x25, 0x38, 0xff}
	if active {
		bg = color.RGBA{0x34, 0x3e, 0x5e, 0xff}
	}
	vector.FillRect(screen, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), bg, false)
	vector.StrokeRect(screen, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()), 1, color.RGBA{0xff, 0xff, 0xff, 0x60}, false)
	drawText(screen, label, 16, float64(r.Min.X+r.Dx()/2), float64(r.Min.Y+10), alignCenter, color.RGBA{0xff, 0xff, 0xff, 0xff})
}
