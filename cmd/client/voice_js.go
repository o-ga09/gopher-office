//go:build js

package main

import (
	"errors"
	"syscall/js"

	"github.com/o-ga09/gopher-office/internal/voice"
)

// webVoice は web/voice.js（window.gopherVoice）を呼ぶ voice.Engine。
type webVoice struct {
	api       js.Value
	onSignal  js.Func
	onClosed  js.Func
	hasHandle bool
}

// newWebVoice は voice.js が読み込まれていれば Engine を返す。無ければ nil（通話の機能は出ない）。
func newWebVoice() voice.Engine {
	api := js.Global().Get("gopherVoice")
	if api.IsUndefined() || api.IsNull() {
		return nil
	}
	return &webVoice{api: api}
}

func (w *webVoice) SetHandler(h voice.Handler) {
	if w.hasHandle {
		w.onSignal.Release()
		w.onClosed.Release()
	}
	w.hasHandle = true
	w.onSignal = js.FuncOf(func(_ js.Value, args []js.Value) any {
		to := uint32(args[0].Int()) //nolint:gosec // pid は小さい正の数
		payload := []byte(args[1].String())
		go h.OnSignal(to, payload) // JS のイベントループを待たせない
		return nil
	})
	w.onClosed = js.FuncOf(func(_ js.Value, args []js.Value) any {
		pid := uint32(args[0].Int()) //nolint:gosec // 同上
		go h.OnPeerClosed(pid)
		return nil
	})
	w.api.Set("onsignal", w.onSignal)
	w.api.Set("onpeerclosed", w.onClosed)
}

func (w *webVoice) Start(done func(error)) {
	var ok, fail js.Func
	release := func() { ok.Release(); fail.Release() }
	ok = js.FuncOf(func(js.Value, []js.Value) any {
		release()
		go done(nil)
		return nil
	})
	fail = js.FuncOf(func(_ js.Value, args []js.Value) any {
		release()
		go done(errors.New(rejectionText(args)))
		return nil
	})
	w.api.Call("start").Call("then", ok, fail)
}

// rejectionText は Promise の拒否理由を、利用者に見せる短い文にする。
func rejectionText(args []js.Value) string {
	if len(args) == 0 {
		return "unknown error"
	}
	e := args[0]
	if name := e.Get("name"); name.Type() == js.TypeString && name.String() == "NotAllowedError" {
		return "microphone permission denied"
	}
	if msg := e.Get("message"); msg.Type() == js.TypeString {
		return msg.String()
	}
	return "unknown error"
}

func (w *webVoice) Stop()                             { w.api.Call("stop") }
func (w *webVoice) Connect(pid uint32, init bool)     { w.api.Call("connect", pid, init) }
func (w *webVoice) Disconnect(pid uint32)             { w.api.Call("disconnect", pid) }
func (w *webVoice) Signal(pid uint32, payload []byte) { w.api.Call("signal", pid, string(payload)) }
func (w *webVoice) SetGain(pid uint32, gain float64)  { w.api.Call("setGain", pid, gain) }
func (w *webVoice) SetMuted(muted bool)               { w.api.Call("setMuted", muted) }
