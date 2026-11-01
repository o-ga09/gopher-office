package s3blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/o-ga09/gopher-office/internal/blob"
)

// fakeS3 は path-style の PUT / GET だけを話す偽の S3。
type fakeS3 struct {
	mu      sync.Mutex
	objs    map[string][]byte
	types   map[string]string
	badHdrs []string
	paths   []string
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	for k := range r.Header {
		if strings.HasPrefix(strings.ToLower(k), "x-amz-checksum") || strings.EqualFold(k, "x-amz-sdk-checksum-algorithm") {
			f.badHdrs = append(f.badHdrs, k)
		}
	}
	switch r.Method {
	case http.MethodPut:
		b, _ := io.ReadAll(r.Body)
		f.objs[r.URL.Path] = b
		f.types[r.URL.Path] = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		b, ok := f.objs[r.URL.Path]
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>x</Message></Error>`))
			return
		}
		_, _ = w.Write(b)
	}
}

func TestStoreAgainstFakeS3(t *testing.T) {
	f := &fakeS3{objs: map[string][]byte{}, types: map[string]string{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	s, err := New(Config{Endpoint: srv.URL, Bucket: "bkt", Region: "us-east-1", AccessKey: "ak", SecretKey: "sk"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if _, err := s.Get(ctx, "icons/x.png"); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("missing: err = %v", err)
	}
	for range 2 { // 冪等
		if err := s.Put(ctx, "icons/x.png", []byte("png-bytes"), "image/png"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Get(ctx, "icons/x.png")
	if err != nil || !bytes.Equal(got, []byte("png-bytes")) {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if f.types["/bkt/icons/x.png"] != "image/png" {
		t.Fatalf("path-style key / content type not as expected: %v %v", f.objs, f.types)
	}
	if len(f.badHdrs) > 0 {
		t.Fatalf("checksum headers must not be sent: %v", f.badHdrs)
	}
}

func TestNewRequiresConfig(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("empty config must fail")
	}
}
