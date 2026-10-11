package realtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"
)

// BenchmarkRoomRelay exercises validation, encoding, fanout and byte accounting.
// It uses synthetic in-memory clients, not sockets or a production server. Each
// iteration drains every recipient; this deliberately measures healthy clients.
func BenchmarkRoomRelay(b *testing.B) {
	for _, kind := range []string{"state", "msg"} {
		for _, members := range []int{2, 20, 200} {
			for _, size := range []int{128, 1024, 16384} {
				b.Run(fmt.Sprintf("%s/members=%d/bytes=%d", kind, members, size), func(b *testing.B) {
					h := NewHub(nil, nil)
					room := &Room{members: map[string]*Client{}, states: map[string]json.RawMessage{}}
					clients := make([]*Client, members)
					for i := range clients {
						c := &Client{hub: h, playerID: fmt.Sprintf("p%d", i), gameID: "benchmark", send: make(chan []byte, 1), room: room}
						clients[i] = c
						room.members[c.playerID] = c
					}
					data := json.RawMessage(append(append([]byte(`{"text":"`), bytes.Repeat([]byte("x"), size-11)...), []byte(`"}`)...))
					samples := make([]int64, b.N)
					b.ReportAllocs()
					b.SetBytes(int64(len(data) * (members - 1)))
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						start := time.Now()
						h.mu.Lock()
						if kind == "state" {
							h.handleRoomState(clients[0], data)
						} else {
							h.handleRoomMsg(clients[0], data)
						}
						h.mu.Unlock()
						for _, c := range clients[1:] {
							c.releaseQueued(<-c.send)
						}
						samples[i] = time.Since(start).Nanoseconds()
					}
					b.StopTimer()
					if h.queuedBytes.Load() != 0 {
						b.Fatal("queued bytes leaked")
					}
					sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
					if len(samples) > 0 {
						b.ReportMetric(float64(samples[(len(samples)-1)*50/100]), "p50-ns")
						b.ReportMetric(float64(samples[(len(samples)-1)*95/100]), "p95-ns")
						b.ReportMetric(float64(samples[(len(samples)-1)*99/100]), "p99-ns")
					}
				})
			}
		}
	}
}
