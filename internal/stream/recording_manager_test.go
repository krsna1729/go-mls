package stream

import (
	"context"
	"sync"
	"testing"
	"time"

	"go-mls/internal/logger"
)

// TestRecordingManager_ConcurrentAPI exercises the public API concurrently to ensure thread safety.
func TestRecordingManager_ConcurrentAPI(t *testing.T) {
	log := logger.NewLogger()
	dir := t.TempDir()
	relayMgr := NewRelayManager(log, dir)
	rm := NewRecordingManager(log, dir, relayMgr)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	num := 10
	var wg sync.WaitGroup
	recNames := make([]string, num)
	for i := 0; i < num; i++ {
		recNames[i] = "rec" + string(rune('A'+i))
	}

	source := "testsrc"

	// Start recordings concurrently
	for i := 0; i < num; i++ {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_ = rm.StartRecording(ctx, name, source) // Use a dummy source
		}(recNames[i])
	}

	// Stop recordings concurrently
	for i := 0; i < num; i++ {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_ = rm.StopRecording(name, source)
		}(recNames[i])
	}

	// List recordings concurrently
	for i := 0; i < num; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = rm.ListRecordings()
		}()
	}

	wg.Wait()
}
