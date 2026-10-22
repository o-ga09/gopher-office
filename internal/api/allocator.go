package api

import "context"

// Allocation は、ルームを担当する GameServer の割り当て結果。
type Allocation struct {
	// GameServer は GameServer の名前（roomToken に載せる）。
	GameServer string
	// URL はクライアントが接続する WebSocket の URL。
	URL string
}

// RoomAllocator はルームに GameServer を割り当てる。
// Agones を使う実装（GameServerAllocation）は P3 で追加する。
type RoomAllocator interface {
	Allocate(ctx context.Context, room *Room) (Allocation, error)
}

// StaticAllocator はすべてのルームを、固定の 1 つの GameServer に割り当てる。
// Agones を入れる前の開発・検証用。
type StaticAllocator struct {
	// URL はクライアントが接続する WebSocket の URL（例: ws://localhost:7654/room）。
	URL string
}

// Allocate は固定の GameServer を返す。
func (a StaticAllocator) Allocate(context.Context, *Room) (Allocation, error) {
	return Allocation{GameServer: "static", URL: a.URL}, nil
}
