package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/o-ga09/gopher-office/internal/agones"
	"github.com/o-ga09/gopher-office/internal/k8s"
)

const (
	allocLockTTL     = 15 * time.Second
	allocWait        = 10 * time.Second // ロックを待つ上限
	allocPoll        = 100 * time.Millisecond
	allocContention  = 3 // Agones が Contention を返したときの再試行回数
	roomMappingTTL   = time.Hour
	gsStateAllocated = "Allocated"
)

// ErrNoCapacity は、割り当てられる（Ready の）GameServer が無いことを表す。
var ErrNoCapacity = errors.New("api: no game server is available")

// KV は割り当ての直列化とキャッシュに使う、Redis の最小のインターフェース。
type KV interface {
	Get(ctx context.Context, key string) (value string, ok bool, err error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	// SetNX はキーが無いときだけ設定し、設定できたかを返す。
	SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error)
	// DelIfEqual は値が一致するときだけ消す（他のプロセスのロックを消さないため）。
	DelIfEqual(ctx context.Context, key, value string) error
}

// KubeAPI は Kubernetes API の呼び出し。*k8s.Client が満たす。
type KubeAPI interface {
	Do(ctx context.Context, method, path string, body, out any) error
}

// AgonesAllocator は、ルームに Agones の GameServer を割り当てる。
//
//   - ルームに割り当て済みの GameServer があれば、それを返す（Redis に名前を覚え、毎回 k8s で生きているかと
//     担当するルームを確かめる。落ちていれば割り当て直す）
//   - 無ければ、Redis のロックでルームごとに直列化し、Fleet の Ready な GameServer を 1 つ割り当てる。
//     ルームの ID は GameServerAllocation の annotation で GameServer に渡る
type AgonesAllocator struct {
	Kube      KubeAPI
	KV        KV
	Namespace string
	Fleet     string // 割り当て対象の Fleet の名前
	// PublicURL はクライアントが接続する WebSocket の URL（ゲートウェイの公開 URL）。
	PublicURL string

	sleep func(time.Duration) // テスト用
}

func (a *AgonesAllocator) wait(d time.Duration) {
	if a.sleep != nil {
		a.sleep(d)
		return
	}
	time.Sleep(d)
}

// Allocate は RoomAllocator を満たす。
func (a *AgonesAllocator) Allocate(ctx context.Context, room *Room) (Allocation, error) {
	if name, ok, err := a.current(ctx, room.ID); err != nil {
		return Allocation{}, err
	} else if ok {
		return Allocation{GameServer: name, URL: a.PublicURL}, nil
	}

	lockKey := "room:" + room.ID + ":lock"
	owner, err := randomToken()
	if err != nil {
		return Allocation{}, err
	}
	deadline := time.Now().Add(allocWait)
	for {
		got, err := a.KV.SetNX(ctx, lockKey, owner, allocLockTTL)
		if err != nil {
			return Allocation{}, err
		}
		if got {
			break
		}
		// 他のリクエストが割り当て中。終わるのを待つ
		if time.Now().After(deadline) {
			return Allocation{}, errors.New("api: timed out waiting for another allocation of the room")
		}
		a.wait(allocPoll)
		if name, ok, err := a.current(ctx, room.ID); err != nil {
			return Allocation{}, err
		} else if ok {
			return Allocation{GameServer: name, URL: a.PublicURL}, nil
		}
	}
	defer func() { _ = a.KV.DelIfEqual(context.WithoutCancel(ctx), lockKey, owner) }()

	// ロックを取るまでの間に、別のリクエストが割り当て終えているかもしれない
	if name, ok, err := a.current(ctx, room.ID); err != nil {
		return Allocation{}, err
	} else if ok {
		return Allocation{GameServer: name, URL: a.PublicURL}, nil
	}

	name, err := a.allocate(ctx, room.ID)
	if err != nil {
		return Allocation{}, err
	}
	if err := a.KV.Set(ctx, mappingKey(room.ID), name, roomMappingTTL); err != nil {
		return Allocation{}, err
	}
	return Allocation{GameServer: name, URL: a.PublicURL}, nil
}

func mappingKey(roomID string) string { return "room:" + roomID }

// current は、ルームに割り当て済みで、まだ生きている GameServer の名前を返す。
func (a *AgonesAllocator) current(ctx context.Context, roomID string) (string, bool, error) {
	name, ok, err := a.KV.Get(ctx, mappingKey(roomID))
	if err != nil || !ok {
		return "", false, err
	}
	var gs struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Status struct {
			State string `json:"state"`
		} `json:"status"`
	}
	path := fmt.Sprintf("/apis/agones.dev/v1/namespaces/%s/gameservers/%s", url.PathEscape(a.Namespace), url.PathEscape(name))
	switch err := a.Kube.Do(ctx, http.MethodGet, path, nil, &gs); {
	case errors.Is(err, k8s.ErrNotFound):
		return "", false, nil // 落ちた（または消えた）。割り当て直す
	case err != nil:
		return "", false, err
	}
	if gs.Status.State != gsStateAllocated || gs.Metadata.Annotations[agones.RoomIDAnnotation] != roomID {
		return "", false, nil
	}
	return name, true, nil
}

// allocate は Fleet の Ready な GameServer を 1 つ、ルームに割り当てる。
func (a *AgonesAllocator) allocate(ctx context.Context, roomID string) (string, error) {
	req := map[string]any{
		"apiVersion": "allocation.agones.dev/v1",
		"kind":       "GameServerAllocation",
		"spec": map[string]any{
			"selectors": []any{map[string]any{
				"matchLabels":     map[string]string{"agones.dev/fleet": a.Fleet},
				"gameServerState": "Ready",
			}},
			"metadata": map[string]any{
				"annotations": map[string]string{agones.RoomIDAnnotation: roomID},
			},
		},
	}
	path := fmt.Sprintf("/apis/allocation.agones.dev/v1/namespaces/%s/gameserverallocations", url.PathEscape(a.Namespace))

	for attempt := 0; ; attempt++ {
		var res struct {
			Status struct {
				State      string `json:"state"`
				GameServer string `json:"gameServerName"`
			} `json:"status"`
		}
		if err := a.Kube.Do(ctx, http.MethodPost, path, req, &res); err != nil {
			return "", err
		}
		switch res.Status.State {
		case gsStateAllocated:
			if res.Status.GameServer == "" {
				return "", errors.New("api: allocation has no game server name")
			}
			return res.Status.GameServer, nil
		case "Contention": // 他の割り当てと競合した。少し待って再試行する
			if attempt+1 >= allocContention {
				return "", fmt.Errorf("api: allocation kept conflicting: %w", ErrNoCapacity)
			}
			a.wait(time.Duration(attempt+1) * allocPoll)
		default: // UnAllocated
			return "", ErrNoCapacity
		}
	}
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
