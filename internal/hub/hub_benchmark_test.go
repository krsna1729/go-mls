package hub

import (
	"testing"

	"go-mls/internal/logger"
)

func BenchmarkHubStartStopRTMP(b *testing.B) {
	log := logger.NewLoggerWithConfig("error", "")
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		h := NewRTMPHub(log, "127.0.0.1", 0)
		if err := h.Start(); err != nil {
			b.Fatalf("start failed: %v", err)
		}
		h.Stop()
	}
}

func BenchmarkHubStartStopRTSP(b *testing.B) {
	log := logger.NewLoggerWithConfig("error", "")
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		h := NewRTSPHub(log, "127.0.0.1", 0)
		if err := h.Start(); err != nil {
			b.Fatalf("start failed: %v", err)
		}
		h.Stop()
	}
}
