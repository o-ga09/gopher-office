package api

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/o-ga09/gopher-office/internal/icon"
)

func testPNG(t *testing.T, w, h int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// doRaw は JSON ではなく、バイト列そのものをボディにして送る。
func (env *testEnv) doRaw(method, path, token string, body []byte, header ...string) response {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.RemoteAddr = "192.0.2.1:1234"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	return response{rec}
}

func TestPutIconStoresA64x64PNGAndSetsTheHash(t *testing.T) {
	env := newTestEnv(t)
	id, token := env.guest(t, "alice", 0)

	res := env.doRaw(http.MethodPut, "/v1/me/icon", token, testPNG(t, 400, 200, color.NRGBA{200, 20, 20, 255}), "Content-Type", "text/plain")
	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body.String())
	}
	hash, _ := res.json(t)["player"].(map[string]any)["icon_hash"].(string)
	if !icon.ValidHash(hash) {
		t.Fatalf("icon_hash = %q", hash)
	}

	me := env.do(t, http.MethodGet, "/v1/me", token, nil).json(t)["player"].(map[string]any)
	if me["icon_hash"] != hash {
		t.Fatalf("GET /v1/me icon_hash = %v, want %s", me["icon_hash"], hash)
	}

	got := env.doRaw(http.MethodGet, "/v1/players/"+id+"/icon", "", nil)
	if got.Code != http.StatusOK || got.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("status %d type %q", got.Code, got.Header().Get("Content-Type"))
	}
	img, err := png.Decode(got.Body)
	if err != nil || img.Bounds().Dx() != icon.Size || img.Bounds().Dy() != icon.Size {
		t.Fatalf("served image: %v %v", img.Bounds(), err)
	}
}

func TestPutIconRejectsBadImages(t *testing.T) {
	env := newTestEnv(t)
	_, token := env.guest(t, "alice", 0)

	tests := []struct {
		name   string
		body   []byte
		status int
		code   string
	}{
		{"画像でない", []byte(strings.Repeat("x", 100)), http.StatusUnsupportedMediaType, "unsupported_image"},
		{"空", nil, http.StatusUnsupportedMediaType, "unsupported_image"},
		{"256KB超", bytes.Repeat([]byte{0}, icon.MaxUploadBytes+1), http.StatusRequestEntityTooLarge, "too_large"},
		{"寸法が大きすぎる", bigDimensionPNG(t), http.StatusBadRequest, "image_dimensions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := env.doRaw(http.MethodPut, "/v1/me/icon", token, tt.body)
			if res.Code != tt.status || res.errorCode(t) != tt.code {
				t.Fatalf("status %d code %q: %s", res.Code, res.errorCode(t), res.Body.String())
			}
		})
	}
}

func bigDimensionPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, icon.MaxDimension+1, 1))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPutIconRequiresAuth(t *testing.T) {
	env := newTestEnv(t)
	if res := env.doRaw(http.MethodPut, "/v1/me/icon", "", testPNG(t, 10, 10, color.NRGBA{A: 255})); res.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", res.Code)
	}
}

// アイコン以外の JSON は 8 KB のままで、アイコンの 256 KB 緩和が他へ広がらない。
func TestIconBodyLimitDoesNotLoosenOtherRoutes(t *testing.T) {
	env := newTestEnv(t)
	_, token := env.guest(t, "alice", 0)
	big := `{"display_name":"` + strings.Repeat("a", 20<<10) + `"}`
	if res := env.doRaw(http.MethodPatch, "/v1/me", token, []byte(big), "Content-Type", "application/json"); res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", res.Code)
	}
}

func TestGetIconCaching(t *testing.T) {
	env := newTestEnv(t)
	id, token := env.guest(t, "alice", 0)
	res := env.doRaw(http.MethodPut, "/v1/me/icon", token, testPNG(t, 64, 64, color.NRGBA{1, 2, 3, 255}))
	hash := res.json(t)["player"].(map[string]any)["icon_hash"].(string)
	path := "/v1/players/" + id + "/icon"

	plain := env.doRaw(http.MethodGet, path, "", nil)
	if plain.Header().Get("ETag") != `"`+hash+`"` || plain.Header().Get("Cache-Control") != cacheRevalidate {
		t.Fatalf("etag %q cache %q", plain.Header().Get("ETag"), plain.Header().Get("Cache-Control"))
	}
	if plain.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("nosniff is required")
	}

	versioned := env.doRaw(http.MethodGet, path+"?h="+hash, "", nil)
	if versioned.Header().Get("Cache-Control") != cacheImmutable {
		t.Fatalf("a URL with the current hash must be immutable, got %q", versioned.Header().Get("Cache-Control"))
	}
	stale := env.doRaw(http.MethodGet, path+"?h="+strings.Repeat("0", 64), "", nil)
	if stale.Code != http.StatusOK || stale.Header().Get("Cache-Control") != cacheRevalidate {
		t.Fatalf("a stale hash must be served without long caching: %d %q", stale.Code, stale.Header().Get("Cache-Control"))
	}

	cond := env.doRaw(http.MethodGet, path, "", nil, "If-None-Match", `"other", "`+hash+`"`)
	if cond.Code != http.StatusNotModified || cond.Body.Len() != 0 {
		t.Fatalf("status %d, body %d bytes", cond.Code, cond.Body.Len())
	}
}

func TestGetIconNotFound(t *testing.T) {
	env := newTestEnv(t)
	id, _ := env.guest(t, "no icon", 0)
	for _, p := range []string{id, "01ARZ3NDEKTSV4RRFFQ69G5FAV", "short", "../../etc/passwd"} {
		if res := env.doRaw(http.MethodGet, "/v1/players/"+p+"/icon", "", nil); res.Code != http.StatusNotFound {
			t.Errorf("%s: status %d", p, res.Code)
		}
	}
}

func TestReuploadingTheSameImageKeepsTheHash(t *testing.T) {
	env := newTestEnv(t)
	_, token := env.guest(t, "alice", 0)
	body := testPNG(t, 100, 100, color.NRGBA{9, 9, 9, 255})
	h := func() string {
		res := env.doRaw(http.MethodPut, "/v1/me/icon", token, body)
		return res.json(t)["player"].(map[string]any)["icon_hash"].(string)
	}
	if a, b := h(), h(); a != b {
		t.Fatalf("hashes differ: %s vs %s", a, b)
	}
}
