package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-mls/internal/logger"
	"go-mls/internal/state"
)

func buildStatsBenchmarkServer() *Server {
	store := state.NewStore()
	for i := 0; i < 300; i++ {
		streamPath := fmt.Sprintf("bench-stream-%d", i)
		_ = store.AddInput(&state.Input{
			StreamPath: streamPath,
			Mode:       state.InputModeAccept,
			Status:     state.InputStatusActive,
			PID:        10000 + i,
		})
		for j := 0; j < 3; j++ {
			outputID := fmt.Sprintf("out-%d", j)
			pid := 20000 + i*10 + j
			_ = store.AddOutput(&state.Output{
				StreamPath: streamPath,
				OutputID:   outputID,
				RemoteURL:  fmt.Sprintf("rtmp://example.com/live/%d/%d", i, j),
				Status:     state.OutputStatusRunning,
				PID:        pid,
			})
			store.UpdateTelemetry(pid, &state.Telemetry{CPU: 1.25, MemMB: 64})
		}
		store.UpdateTelemetry(10000+i, &state.Telemetry{CPU: 0.75, MemMB: 48})
	}

	return NewServer(
		store,
		nil,
		nil,
		logger.NewLogger(),
		"",
		"",
		1935,
		"",
		context.Background(),
	)
}

func BenchmarkHandleStats(b *testing.B) {
	s := buildStatsBenchmarkServer()
	b.Cleanup(s.Shutdown)
	req := httptest.NewRequest(http.MethodGet, "/stats", nil)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		s.handleStats(rr, req)
		if rr.Code != http.StatusOK {
			b.Fatalf("unexpected status: %d", rr.Code)
		}
	}
}
