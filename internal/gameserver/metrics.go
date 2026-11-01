package gameserver

import (
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// metrics はルームの計測値。複数の goroutine（tick・各接続の書き込み・メトリクスの読み出し）から触るので atomic にする。
type metrics struct {
	bytesSent  atomic.Int64 // クライアントへ書いたペイロードの合計（WebSocket のフレームヘッダは含まない）
	msgsSent   atomic.Int64
	dropped    atomic.Int64 // 遅い接続のために捨てた（droppable な）メッセージ
	kicked     atomic.Int64 // 遅すぎて追い出した接続
	tickNanos  atomic.Int64 // tick の処理時間の合計
	ticks      atomic.Int64
	tickMaxNs  atomic.Int64 // 前回の読み出し以降の、tick の処理時間の最大
	tickOverNs atomic.Int64 // tick の間隔（50ms）を超えた回数
}

func (m *metrics) observeTick(d time.Duration, budget time.Duration) {
	ns := d.Nanoseconds()
	m.tickNanos.Add(ns)
	m.ticks.Add(1)
	if d > budget {
		m.tickOverNs.Add(1)
	}
	for {
		cur := m.tickMaxNs.Load()
		if ns <= cur || m.tickMaxNs.CompareAndSwap(cur, ns) {
			return
		}
	}
}

// WriteMetrics は計測値を Prometheus のテキスト形式で書く。
// office_tick_max_seconds は「前回の読み出し以降」の最大で、読み出すたびにリセットされる。
func (r *Room) WriteMetrics(w io.Writer) {
	m := &r.m
	fmt.Fprintf(w, "# TYPE office_players gauge\noffice_players %d\n", r.Players())
	fmt.Fprintf(w, "# TYPE office_sent_bytes_total counter\noffice_sent_bytes_total %d\n", m.bytesSent.Load())
	fmt.Fprintf(w, "# TYPE office_sent_messages_total counter\noffice_sent_messages_total %d\n", m.msgsSent.Load())
	fmt.Fprintf(w, "# TYPE office_dropped_messages_total counter\noffice_dropped_messages_total %d\n", m.dropped.Load())
	fmt.Fprintf(w, "# TYPE office_kicked_total counter\noffice_kicked_total %d\n", m.kicked.Load())
	fmt.Fprintf(w, "# TYPE office_ticks_total counter\noffice_ticks_total %d\n", m.ticks.Load())
	fmt.Fprintf(w, "# TYPE office_tick_seconds_total counter\noffice_tick_seconds_total %g\n", float64(m.tickNanos.Load())/1e9)
	fmt.Fprintf(w, "# TYPE office_tick_overruns_total counter\noffice_tick_overruns_total %d\n", m.tickOverNs.Load())
	fmt.Fprintf(w, "# TYPE office_tick_max_seconds gauge\noffice_tick_max_seconds %g\n", float64(m.tickMaxNs.Swap(0))/1e9)
}

// Stats は計測値のスナップショット。
type Stats struct {
	Players   int
	BytesSent int64
	Ticks     int64
	TickTime  time.Duration // tick の処理時間の合計
	Overruns  int64         // tick の間隔を超えた回数
	Dropped   int64
	Kicked    int64
}

// Stats は計測値を返す（最大値のリセットはしない）。
func (r *Room) Stats() Stats {
	m := &r.m
	return Stats{
		Players: r.Players(), BytesSent: m.bytesSent.Load(), Ticks: m.ticks.Load(),
		TickTime: time.Duration(m.tickNanos.Load()), Overruns: m.tickOverNs.Load(),
		Dropped: m.dropped.Load(), Kicked: m.kicked.Load(),
	}
}
