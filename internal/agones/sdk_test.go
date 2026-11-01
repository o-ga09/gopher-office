package agones

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestReadyHealthShutdownPostToTheSidecar(t *testing.T) {
	var mu sync.Mutex
	var got []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.Path)
		mu.Unlock()
		_, _ = w.Write([]byte("{}"))
	}))
	defer ts.Close()

	c := New(ts.URL + "/")
	ctx := context.Background()
	for _, f := range []func(context.Context) error{c.Ready, c.Health, c.Shutdown} {
		if err := f(ctx); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"POST /ready", "POST /health", "POST /shutdown"}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestSDKErrorsAreReported(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()
	if err := New(ts.URL).Ready(context.Background()); err == nil {
		t.Fatal("a 500 must be an error")
	}
}

func TestWatchReportsAnnotationsAndReconnects(t *testing.T) {
	conns := make(chan struct{}, 4)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conns <- struct{}{}
		_, _ = io.WriteString(w, `{"result":{"object_meta":{"annotations":null},"status":{"state":"Ready"}}}`+"\n")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, `{"result":{"object_meta":{"annotations":{"roomId":"R1"}},"status":{"state":"Allocated"}}}`+"\n")
		// ここで切る → クライアントは張り直す
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	states := make(chan State, 8)
	go New(ts.URL).Watch(ctx, quiet, func(s State) { states <- s })

	if s := <-states; s.Status != "Ready" || s.RoomID != "" {
		t.Fatalf("first = %+v", s)
	}
	if s := <-states; s.Status != "Allocated" || s.RoomID != "R1" {
		t.Fatalf("second = %+v", s)
	}
	<-conns
	select {
	case <-conns: // 2 回目の接続
	case <-time.After(3 * time.Second):
		t.Fatal("the watch must reconnect")
	}
}

func TestHealthLoopStopsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { New("http://127.0.0.1:1").HealthLoop(ctx, quiet); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("HealthLoop did not stop")
	}
}
