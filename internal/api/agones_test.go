package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/o-ga09/gopher-office/internal/k8s"
)

type memKV struct {
	mu   sync.Mutex
	data map[string]string
}

func newMemKV() *memKV { return &memKV{data: map[string]string{}} }

func (k *memKV) Get(_ context.Context, key string) (string, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.data[key]
	return v, ok, nil
}

func (k *memKV) Set(_ context.Context, key, v string, _ time.Duration) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.data[key] = v
	return nil
}

func (k *memKV) SetNX(_ context.Context, key, v string, _ time.Duration) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.data[key]; ok {
		return false, nil
	}
	k.data[key] = v
	return true, nil
}

func (k *memKV) DelIfEqual(_ context.Context, key, v string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.data[key] == v {
		delete(k.data, key)
	}
	return nil
}

// fakeKube は Agones の GameServer と GameServerAllocation だけを模した k8s API。
type fakeKube struct {
	mu          sync.Mutex
	gameservers map[string]map[string]any // name -> {state, roomId}
	ready       []string                  // 割り当て可能な GameServer の名前
	allocations int
	contention  int // この回数だけ Contention を返す
}

func newFakeKube(ready ...string) *fakeKube {
	return &fakeKube{gameservers: map[string]map[string]any{}, ready: ready}
}

func (f *fakeKube) Do(_ context.Context, method, path string, body, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case method == http.MethodGet && strings.Contains(path, "/gameservers/"):
		name := path[strings.LastIndex(path, "/")+1:]
		gs, ok := f.gameservers[name]
		if !ok {
			return k8s.ErrNotFound
		}
		o := out.(*struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Status struct {
				State string `json:"state"`
			} `json:"status"`
		})
		o.Status.State = gs["state"].(string)
		o.Metadata.Annotations = map[string]string{"roomId": gs["roomId"].(string)}
		return nil
	case method == http.MethodPost && strings.HasSuffix(path, "/gameserverallocations"):
		f.allocations++
		res := out.(*struct {
			Status struct {
				State      string `json:"state"`
				GameServer string `json:"gameServerName"`
			} `json:"status"`
		})
		if f.contention > 0 {
			f.contention--
			res.Status.State = "Contention"
			return nil
		}
		if len(f.ready) == 0 {
			res.Status.State = "UnAllocated"
			return nil
		}
		name := f.ready[0]
		f.ready = f.ready[1:]
		spec := body.(map[string]any)["spec"].(map[string]any)
		room := spec["metadata"].(map[string]any)["annotations"].(map[string]string)["roomId"]
		f.gameservers[name] = map[string]any{"state": "Allocated", "roomId": room}
		res.Status.State, res.Status.GameServer = "Allocated", name
		return nil
	}
	return errors.New("unexpected request " + method + " " + path)
}

func newAllocator(kube KubeAPI, kv KV) *AgonesAllocator {
	return &AgonesAllocator{Kube: kube, KV: kv, Namespace: "gopher-office", Fleet: "office", PublicURL: "wss://office.example/room", sleep: func(time.Duration) {}}
}

func TestAllocateAssignsOnceAndReusesTheServer(t *testing.T) {
	kube := newFakeKube("gs-1", "gs-2")
	a := newAllocator(kube, newMemKV())
	room := &Room{ID: "R1"}

	first, err := a.Allocate(context.Background(), room)
	if err != nil || first.GameServer != "gs-1" || first.URL != "wss://office.example/room" {
		t.Fatalf("first = %+v err=%v", first, err)
	}
	again, err := a.Allocate(context.Background(), room)
	if err != nil || again.GameServer != "gs-1" {
		t.Fatalf("a second join must reuse the server: %+v err=%v", again, err)
	}
	if kube.allocations != 1 {
		t.Fatalf("allocations = %d, want 1", kube.allocations)
	}
	other, _ := a.Allocate(context.Background(), &Room{ID: "R2"})
	if other.GameServer != "gs-2" {
		t.Fatalf("another room gets another server: %+v", other)
	}
}

func TestAllocateReallocatesWhenTheServerIsGone(t *testing.T) {
	kube := newFakeKube("gs-1", "gs-2")
	a := newAllocator(kube, newMemKV())
	room := &Room{ID: "R1"}
	if _, err := a.Allocate(context.Background(), room); err != nil {
		t.Fatal(err)
	}
	delete(kube.gameservers, "gs-1") // GameServer が落ちた

	got, err := a.Allocate(context.Background(), room)
	if err != nil || got.GameServer != "gs-2" {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestAllocateDoesNotTrustAServerForAnotherRoom(t *testing.T) {
	kube := newFakeKube("gs-2")
	kv := newMemKV()
	kube.gameservers["gs-1"] = map[string]any{"state": "Allocated", "roomId": "SOMEONE-ELSE"}
	_ = kv.Set(context.Background(), mappingKey("R1"), "gs-1", time.Hour)

	got, err := newAllocator(kube, kv).Allocate(context.Background(), &Room{ID: "R1"})
	if err != nil || got.GameServer != "gs-2" {
		t.Fatalf("a stale mapping must not be used: %+v err=%v", got, err)
	}
}

func TestAllocateConcurrentJoinsShareOneServer(t *testing.T) {
	kube := newFakeKube("gs-1", "gs-2", "gs-3")
	kv := newMemKV()
	a := newAllocator(kube, kv)
	a.sleep = func(time.Duration) { time.Sleep(time.Millisecond) }

	var wg sync.WaitGroup
	names := make(chan string, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := a.Allocate(context.Background(), &Room{ID: "R1"})
			if err != nil {
				t.Error(err)
				return
			}
			names <- got.GameServer
		}()
	}
	wg.Wait()
	close(names)
	for n := range names {
		if n != "gs-1" {
			t.Fatalf("every join must land on the same server, got %s", n)
		}
	}
	if kube.allocations != 1 {
		t.Fatalf("allocations = %d, want 1", kube.allocations)
	}
	if len(kv.data) != 1 { // ロックは解放され、割り当ての記録だけが残る
		t.Fatalf("redis keys = %v", kv.data)
	}
}

func TestAllocateWithoutCapacity(t *testing.T) {
	a := newAllocator(newFakeKube(), newMemKV())
	if _, err := a.Allocate(context.Background(), &Room{ID: "R1"}); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("err = %v", err)
	}
}

func TestAllocateRetriesOnContention(t *testing.T) {
	kube := newFakeKube("gs-1")
	kube.contention = 2
	got, err := newAllocator(kube, newMemKV()).Allocate(context.Background(), &Room{ID: "R1"})
	if err != nil || got.GameServer != "gs-1" {
		t.Fatalf("got %+v err=%v", got, err)
	}

	kube = newFakeKube("gs-1")
	kube.contention = 10
	if _, err := newAllocator(kube, newMemKV()).Allocate(context.Background(), &Room{ID: "R1"}); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("persistent contention: err = %v", err)
	}
}

func TestAllocateReleasesTheLockOnFailure(t *testing.T) {
	kv := newMemKV()
	a := newAllocator(newFakeKube(), kv)
	_, _ = a.Allocate(context.Background(), &Room{ID: "R1"})
	if _, ok, _ := kv.Get(context.Background(), "room:R1:lock"); ok {
		t.Fatal("the lock must be released even when allocation fails")
	}
}
