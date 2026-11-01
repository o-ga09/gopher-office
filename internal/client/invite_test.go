package client

import "testing"

func TestTokenQuery(t *testing.T) {
	for _, c := range []struct{ url, token, want string }{
		{"wss://office.example/room", "a.b.c", "?token=a.b.c"},
		{"ws://localhost:7654/room?x=1", "a b", "&token=a+b"},
	} {
		if got := tokenQuery(c.url, c.token); got != c.want {
			t.Errorf("tokenQuery(%q, %q) = %q, want %q", c.url, c.token, got, c.want)
		}
	}
}
