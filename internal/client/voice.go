package client

import (
	"image/color"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"

	officev1 "github.com/o-ga09/gopher-office/gen/office/v1"
	"github.com/o-ga09/gopher-office/internal/netcode"
	"github.com/o-ga09/gopher-office/internal/sim"
	"github.com/o-ga09/gopher-office/internal/voice"
)

// voiceEvery は、近くの人の一覧を通話に渡す間隔（tick）。10 tick = 0.5 秒。
const voiceEvery = 10

// signalSender は通話のシグナリングをサーバへ送る関数を作る（別の goroutine から呼ばれるので、接続を捕まえておく）。
func signalSender(c *netcode.Client) func(to uint32, payload []byte) {
	return func(to uint32, payload []byte) {
		c.Send(&officev1.ClientMessage{Body: &officev1.ClientMessage_Signal{Signal: &officev1.Signal{Pid: to, Payload: payload}}})
	}
}

// handleSignal は、サーバから届いたシグナリングを通話に渡す。シグナリングなら true。
func (g *Game) handleSignal(m *officev1.ServerMessage) bool {
	sig := m.GetSignal()
	if sig == nil {
		return false
	}
	if g.voice != nil {
		g.voice.HandleSignal(sig.GetPid(), sig.GetPayload())
	}
	return true
}

func (g *Game) closeVoice() {
	if g.voice != nil {
		g.voice.Close()
		g.voice = nil
	}
}

// updateVoiceKeys は V（通話の開始・終了）、M（ミュート）、C（カメラ）を処理する。
// 通話は、利用者が V を押したときだけ始まる（マイクの許可を求める）。
func (g *Game) updateVoiceKeys() {
	if g.voice == nil {
		return
	}
	if p := g.w.Self(); p != nil {
		g.voice.SetSelf(p.PID)
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyV) {
		g.voice.Toggle()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyM) {
		g.voice.ToggleMute()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyC) {
		g.voice.ToggleCamera()
	}
}

// updateVoice は tick ごとに呼び、一定間隔で近くの人を通話に渡す。
func (g *Game) updateVoice() {
	if g.voice == nil {
		return
	}
	if g.voiceNow++; g.voiceNow < voiceEvery {
		return
	}
	g.voiceNow = 0
	g.voice.Update(neighbors(g.w))
}

// neighbors は、自分から見た他の人と距離（タイル）。
func neighbors(w *netcode.World) []voice.Neighbor {
	self := w.Self()
	if self == nil {
		return nil
	}
	out := make([]voice.Neighbor, 0, len(w.Players))
	for _, p := range w.Players {
		if p == self {
			continue
		}
		dx := float64(p.Actor.X-self.Actor.X) / sim.SubPerTile
		dy := float64(p.Actor.Y-self.Actor.Y) / sim.SubPerTile
		out = append(out, voice.Neighbor{PID: p.PID, Name: p.Name, Dist: math.Hypot(dx, dy)})
	}
	return out
}

// voiceHUD は画面上部に出す通話の案内。
func (g *Game) voiceHUD() string {
	if g.voice == nil {
		return ""
	}
	st, err := g.voice.State()
	switch st {
	case voice.Starting:
		return "   Voice: allow the microphone..."
	case voice.On:
		s := "   Voice ON (V: leave, M: mute"
		if g.voice.Muted() {
			s = "   Voice ON - muted (V: leave, M: unmute"
		}
		cam, err := g.voice.Camera()
		switch {
		case cam:
			s += ", C: camera off)"
		case err != nil:
			s += ", C: camera failed: " + err.Error() + ")"
		default:
			s += ", C: camera)"
		}
		return s
	case voice.Failed:
		return "   Voice failed: " + err.Error() + " (V: retry)"
	}
	return "   V: voice chat"
}

// drawVoiceRange は、通話中に声の届く範囲を自分の周りに薄く描く。
func (g *Game) drawVoiceRange(screen *ebiten.Image, self *netcode.Player, alpha float64) {
	if g.voice == nil {
		return
	}
	if st, _ := g.voice.State(); st != voice.On {
		return
	}
	x, y := self.RenderPos(alpha)
	vector.StrokeCircle(screen, float32(x*subToPx), float32(y*subToPx+mapOffsetY-tilePx/2), float32(voice.HearRadius*tilePx), 1,
		color.RGBA{0x7d, 0xd3, 0xfc, 0x50}, true)
}
