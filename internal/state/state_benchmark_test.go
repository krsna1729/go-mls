package state

import (
	"fmt"
	"sync/atomic"
	"testing"
)

func buildBenchmarkStore(inputs, outputs, recordings int) *Store {
	s := NewStore()
	for i := 0; i < inputs; i++ {
		streamPath := fmt.Sprintf("stream-%d", i)
		_ = s.AddInput(&Input{
			StreamPath: streamPath,
			Mode:       InputModeAccept,
			Status:     InputStatusActive,
			PID:        1000 + i,
		})
	}
	for i := 0; i < outputs; i++ {
		streamPath := fmt.Sprintf("stream-%d", i%inputs)
		_ = s.AddOutput(&Output{
			StreamPath: streamPath,
			OutputID:   fmt.Sprintf("out-%d", i),
			RemoteURL:  fmt.Sprintf("rtmp://example.com/live/%d", i),
			Status:     OutputStatusRunning,
			PID:        2000 + i,
		})
	}
	for i := 0; i < recordings; i++ {
		_ = s.AddRecording(&Recording{
			StreamPath: fmt.Sprintf("recording-stream-%d", i),
			Filename:   fmt.Sprintf("stream-%d_%d.mp4", i%inputs, i),
			Status:     RecordingStatusActive,
			PID:        3000 + i,
		})
	}
	return s
}

func BenchmarkStoreListSnapshots(b *testing.B) {
	s := buildBenchmarkStore(1000, 4000, 1000)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = s.ListInputs()
		_ = s.ListOutputs()
		_ = s.ListRecordings()
	}
}

func BenchmarkStoreListOutputsParallelWithUpdates(b *testing.B) {
	s := buildBenchmarkStore(500, 2000, 500)
	var ctr atomic.Uint64

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := ctr.Add(1)
			idx := int(n % 2000)
			streamPath := fmt.Sprintf("stream-%d", idx%500)
			outputID := fmt.Sprintf("out-%d", idx)
			s.UpdateOutputStatus(streamPath, outputID, OutputStatusRunning, "")
			_ = s.ListOutputs()
		}
	})
}
