package stream

import (
	"context"
	"math"
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

// Ensure very long lines are handled (truncated) and do not cause scanner errors/panics.
func TestFFmpegProcess_captureOutput_LongLine_Truncate(t *testing.T) {
	ctx := context.Background()
	proc, _ := NewFFmpegProcess(ctx)
	p := proc.(*ffmpegProcess)

	// Create a line longer than the configured maxLineLen (256KB)
	longLen := 300 * 1024 // 300 KB
	longLine := strings.Repeat("A", longLen) + "\n"
	// Add a short line after it
	input := longLine + "short_line\n"
	r := strings.NewReader(input)

	// Call captureOutput directly (synchronous for reader)
	p.captureOutput(r)

	// Ensure we captured the last 2 lines and that the first one was truncated
	lines := p.GetLastOutputLines(2)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "(truncated)") {
		t.Fatalf("expected truncated marker in first long line, got len=%d", len(lines[0]))
	}
	if lines[1] != "short_line" {
		t.Fatalf("expected second line 'short_line', got %q", lines[1])
	}
}

// Ensure parseProgress tolerates long noise before progress lines and still parses values.
func TestFFmpegProcess_parseProgress_LongNoiseThenValues(t *testing.T) {
	ctx := context.Background()
	proc, _ := NewFFmpegProcess(ctx, "-progress", "pipe:1")
	p := proc.(*ffmpegProcess)

	// Large noise line followed by progress key/value lines
	noise := strings.Repeat("X", 100*1024) + "\n"
	progress := "speed=2.50x\nbitrate=1234.5kbits/s\n"
	r := strings.NewReader(noise + progress)

	// parseProgress is synchronous for this reader
	p.parseProgress(r)

	speed, _ := p.GetSpeed()
	bitrate, ok := p.GetBitrate()
	if !ok {
		t.Fatalf("expected bitrate to be parsed")
	}
	if math.Abs(speed-2.5) > 0.0001 {
		t.Fatalf("expected speed ~2.5, got %v", speed)
	}
	if math.Abs(bitrate-1234.5) > 0.01 {
		t.Fatalf("expected bitrate ~1234.5, got %v", bitrate)
	}
}

// The following tests are removed or rewritten to use only the public interface:
// - Tests that instantiate FFmpegProcess with struct literals
// - Tests that access private fields directly
// For Stop/Wait, use a test double if needed, or test via the interface only.
