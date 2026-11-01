//go:build js

package main

import (
	"errors"
	"syscall/js"
)

const (
	// pickedIconMaxSide は、選んだ画像を縮小する一辺の上限（px）。スマホの写真のような大きな画像も、
	// アップロードの上限（256 KB）に収まるようにここで縮める。サーバが最終的に 64x64 にする。
	pickedIconMaxSide = 256
)

// filePicker はブラウザのファイル選択で、アイコン画像を選ばせる。
// 選ばれた画像は、ブラウザがデコードできる形式なら何でも、中央の正方形を切り出して PNG にする。
type filePicker struct{}

// Pick は選択ダイアログを開く。クリックなどの操作の直後（ユーザーの操作として扱われる間）に呼ぶこと。
func (filePicker) Pick(done func(pngData []byte, err error)) {
	doc := js.Global().Get("document")
	input := doc.Call("createElement", "input")
	input.Set("type", "file")
	input.Set("accept", "image/*")

	var onChange js.Func
	onChange = js.FuncOf(func(js.Value, []js.Value) any {
		defer onChange.Release()
		files := input.Get("files")
		if files.Length() == 0 {
			return nil // 取り消された
		}
		file := files.Index(0)
		go func() { // 待つ処理があるので、JS のコールバックの外で行う
			data, err := squarePNG(file)
			done(data, err)
		}()
		return nil
	})
	input.Call("addEventListener", "change", onChange)
	input.Call("click")
}

// squarePNG は画像ファイルの中央を正方形に切り出し、一辺 pickedIconMaxSide 以下の PNG にして返す。
func squarePNG(file js.Value) ([]byte, error) {
	bmp, err := await(js.Global().Call("createImageBitmap", file))
	if err != nil {
		return nil, err
	}
	defer bmp.Call("close")

	w, h := bmp.Get("width").Int(), bmp.Get("height").Int()
	side := min(w, h)
	if side <= 0 {
		return nil, errors.New("empty image")
	}
	out := min(side, pickedIconMaxSide)

	canvas := js.Global().Get("document").Call("createElement", "canvas")
	canvas.Set("width", out)
	canvas.Set("height", out)
	ctx := canvas.Call("getContext", "2d")
	ctx.Set("imageSmoothingQuality", "high")
	ctx.Call("drawImage", bmp, (w-side)/2, (h-side)/2, side, side, 0, 0, out, out)

	blobCh := make(chan js.Value, 1)
	cb := js.FuncOf(func(_ js.Value, args []js.Value) any {
		blobCh <- args[0]
		return nil
	})
	defer cb.Release()
	canvas.Call("toBlob", cb, "image/png")
	blob := <-blobCh
	if blob.IsNull() {
		return nil, errors.New("could not encode the image")
	}

	buf, err := await(blob.Call("arrayBuffer"))
	if err != nil {
		return nil, err
	}
	arr := js.Global().Get("Uint8Array").New(buf)
	data := make([]byte, arr.Get("length").Int())
	js.CopyBytesToGo(data, arr)
	return data, nil
}

// await は JavaScript の Promise の結果を待つ。goroutine から呼ぶこと（JS のコールバックの中では呼ばない）。
func await(p js.Value) (js.Value, error) {
	type result struct {
		v   js.Value
		err error
	}
	ch := make(chan result, 1)
	then := js.FuncOf(func(_ js.Value, args []js.Value) any {
		ch <- result{v: args[0]}
		return nil
	})
	catch := js.FuncOf(func(_ js.Value, args []js.Value) any {
		ch <- result{err: errors.New(args[0].Call("toString").String())}
		return nil
	})
	defer then.Release()
	defer catch.Release()
	p.Call("then", then).Call("catch", catch)
	r := <-ch
	return r.v, r.err
}
