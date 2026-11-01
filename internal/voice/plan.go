// Package voice は近接音声通話の、ブラウザに依存しない部分（誰とつなぐか・音量をいくつにするか・シグナリングの振り分け）。
// WebRTC 本体はブラウザ標準のものを JS のラッパー経由で使う（web/voice.js、internal/client/voice_js.go）。
// 設計: docs/design.md §11
package voice

import "sort"

const (
	// HearRadius は、この距離（タイル）以内にいる人とつなぎ始める。これより遠いと音量は 0。
	HearRadius = 8.0
	// DropRadius は、つないだ相手をこの距離（タイル）より遠くなるまで切らない（境目で接続が出入りしないように）。
	DropRadius = 10.0
	// FullVolumeRadius は、この距離（タイル）以内なら最大音量。
	FullVolumeRadius = 2.0
	// MaxPeers は同時につなぐ相手の上限。P2P メッシュは接続数が人数の 2 乗で増えるため、近い順に絞る。
	MaxPeers = 5
)

// Neighbor は自分から見た他の人。Dist は自分からの距離（タイル）。
type Neighbor struct {
	PID  uint32
	Dist float64
}

// Gain は距離に応じた音量（0〜1）。FullVolumeRadius までは 1、HearRadius で 0 になる直線の減衰。
func Gain(dist float64) float64 {
	switch {
	case dist <= FullVolumeRadius:
		return 1
	case dist >= HearRadius:
		return 0
	}
	return (HearRadius - dist) / (HearRadius - FullVolumeRadius)
}

// Plan は、今つないでいる相手の集合から、つなぐ・切る相手と各相手の音量を決める。
type Plan struct {
	Connect    []uint32
	Disconnect []uint32
	Gains      map[uint32]float64 // つないでいる（つなぐ）全員の音量
}

// planner は、つないでいる相手を覚えて Plan を作る。
type planner struct {
	connected map[uint32]bool
}

func newPlanner() *planner { return &planner{connected: map[uint32]bool{}} }

// plan は近くにいる人の一覧から次の接続の集合を決めて、planner の状態を更新する。
// 既につないだ相手は DropRadius まで残し、空きがあれば近い順に HearRadius 以内の人を足す。
func (p *planner) plan(ns []Neighbor, blocked func(uint32) bool) Plan {
	sorted := append([]Neighbor(nil), ns...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Dist != sorted[j].Dist {
			return sorted[i].Dist < sorted[j].Dist
		}
		return sorted[i].PID < sorted[j].PID
	})

	next := map[uint32]bool{}
	dist := map[uint32]float64{}
	for _, n := range sorted {
		dist[n.PID] = n.Dist
	}
	for _, n := range sorted { // 既存の接続を優先して残す
		if p.connected[n.PID] && n.Dist <= DropRadius && len(next) < MaxPeers {
			next[n.PID] = true
		}
	}
	for _, n := range sorted {
		if len(next) >= MaxPeers {
			break
		}
		if !next[n.PID] && !p.connected[n.PID] && n.Dist <= HearRadius && (blocked == nil || !blocked(n.PID)) {
			next[n.PID] = true
		}
	}

	out := Plan{Gains: map[uint32]float64{}}
	for pid := range next {
		if !p.connected[pid] {
			out.Connect = append(out.Connect, pid)
		}
		out.Gains[pid] = Gain(dist[pid])
	}
	for pid := range p.connected {
		if !next[pid] {
			out.Disconnect = append(out.Disconnect, pid)
		}
	}
	sort.Slice(out.Connect, func(i, j int) bool { return out.Connect[i] < out.Connect[j] })
	sort.Slice(out.Disconnect, func(i, j int) bool { return out.Disconnect[i] < out.Disconnect[j] })
	p.connected = next
	return out
}
