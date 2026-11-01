package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestFailuresTopOrdersByCountAndStage(t *testing.T) {
	var f failures
	for i := 0; i < 3; i++ {
		f.add("websocket", errors.New("expected handshake response status code 101 but got 401"))
	}
	f.add("join", errors.New("api error 503 unavailable: no room"))
	f.add("join", errors.New("api error 503 unavailable: no room"))
	f.add("create guest", errors.New("api error 429 rate_limited: too many requests"))

	want := []string{
		"3 x websocket: expected handshake response status code 101 but got 401",
		"2 x join: api error 503 unavailable: no room",
	}
	if got := f.top(2); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if got := f.top(10); len(got) != 3 {
		t.Fatalf("all reasons must be reported, got %q", got)
	}
}

func TestFailuresKeepOneLineAndShortReasons(t *testing.T) {
	var f failures
	f.add("websocket", errors.New("first line\nsecond line with a secret=abc"))
	f.add("join", errors.New(strings.Repeat("x", 1000)))

	for _, line := range f.top(10) {
		if strings.Contains(line, "second line") || strings.Contains(line, "\n") {
			t.Errorf("only the first line must be kept: %q", line)
		}
		if n := len([]rune(line)); n > maxReasonRunes+40 {
			t.Errorf("a reason must be cut to a short length, got %d runes", n)
		}
	}
}

func TestFailuresAreSafeForConcurrentUse(t *testing.T) {
	var f failures
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 200; j++ {
				f.add("websocket", errors.New("boom"))
				_ = f.top(3)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if got := f.top(1); len(got) != 1 || !strings.HasPrefix(got[0], "1600 x ") {
		t.Fatalf("got %q", got)
	}
}
