package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/o-ga09/gopher-office/internal/sim"
)

type memStore struct{ tok string }

func (m *memStore) Load() string  { return m.tok }
func (m *memStore) Save(t string) { m.tok = t }

func TestEnsureSession(t *testing.T) {
	tests := []struct {
		name      string
		stored    string
		validTok  string // サーバが受け入れるトークン
		wantTok   string // 終了後の保存トークン
		wantGuest bool   // ゲストを新規作成するか
	}{
		{"no stored token creates guest", "", "good", "new-token", true},
		{"valid stored token is restored", "good", "good", "good", false},
		{"unauthorized token is replaced", "stale", "good", "new-token", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/v1/guest":
					guests++
					_, _ = w.Write([]byte(`{"player":{"id":"p2","display_name":"n","gender":1},"player_token":"new-token"}`))
				case r.Method == http.MethodPatch && r.URL.Path == "/v1/me":
					if r.Header.Get("Authorization") != "Bearer "+tt.validTok {
						w.WriteHeader(http.StatusUnauthorized)
						_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "unauthorized"}})
						return
					}
					_, _ = w.Write([]byte(`{"player":{"id":"p1","display_name":"n","gender":1}}`))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()

			store := &memStore{tok: tt.stored}
			s := newSession(Config{APIBase: srv.URL, Store: store}, "n", sim.Gender(1), nil)
			api, me, err := s.ensureSession(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if (guests == 1) != tt.wantGuest {
				t.Errorf("guests created = %d, want new guest %v", guests, tt.wantGuest)
			}
			if store.tok != tt.wantTok || api.Token() != tt.wantTok {
				t.Errorf("token store=%q api=%q, want %q", store.tok, api.Token(), tt.wantTok)
			}
			if me.ID == "" {
				t.Error("empty player")
			}
		})
	}
}

func TestJoinWithRoomIDUnsupported(t *testing.T) {
	s := newSession(Config{APIBase: "http://127.0.0.1:0"}, "n", 0, nil)
	if _, err := s.join(context.Background(), "code", "room1"); err == nil {
		t.Error("want error for roomID before API support")
	}
}
