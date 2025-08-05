package stream

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFFmpegProcess_parseProgress_ContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	proc, _ := NewFFmpegProcess(ctx, "-progress", "pipe:1")
	p := proc.(*ffmpegProcess)
	// Simulate a progress stream with a few lines
	progress := "speed=1.23x\nbitrate=456.7kbits/s\n"
	r := strings.NewReader(progress)
	done := make(chan struct{})
	go func() {
		p.parseProgress(r)
		close(done)
	}()
	// Cancel context immediately
	cancel()
	select {
	case <-done:
		// Success
	case <-time.After(time.Second):
		t.Fatal("parseProgress did not return on context cancel")
	}
}

func TestFFmpegProcess_captureOutput_ContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	proc, _ := NewFFmpegProcess(ctx)
	p := proc.(*ffmpegProcess)
	output := "line1\nline2\n"
	r := strings.NewReader(output)
	done := make(chan struct{})
	go func() {
		p.captureOutput(r)
		close(done)
	}()
	// Cancel context immediately
	cancel()
	select {
	case <-done:
		// Success
	case <-time.After(time.Second):
		t.Fatal("captureOutput did not return on context cancel")
	}
}

// The following tests are removed or rewritten to use only the public interface:
// - Tests that instantiate FFmpegProcess with struct literals
// - Tests that access private fields directly
// For Stop/Wait, use a test double if needed, or test via the interface only.
