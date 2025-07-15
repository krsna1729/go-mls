package stream

import (
	"context"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestFFmpegProcess_parseProgress_ContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &FFmpegProcess{
		Ctx: ctx,
	}
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
	p := &FFmpegProcess{
		Ctx: ctx,
	}
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

func TestFFmpegProcess_StatsAccessors(t *testing.T) {
	p := &FFmpegProcess{}
	// Set initial stats
	p.SetStats(2.5, 1234.0)

	speed, lastSpeed := p.GetSpeed()
	bitrate, lastBitrate := p.GetBitrate()

	if speed != 2.5 {
		t.Errorf("expected speed 2.5, got %v", speed)
	}
	if bitrate != 1234.0 {
		t.Errorf("expected bitrate 1234.0, got %v", bitrate)
	}
	if lastSpeed.IsZero() {
		t.Error("expected lastSpeed to be set")
	}
	if !lastBitrate {
		t.Error("expected lastBitrate to be set")
	}

	// Update stats again and check
	p.SetStats(3.3, 4321.0)
	speed2, _ := p.GetSpeed()
	bitrate2, _ := p.GetBitrate()
	if speed2 != 3.3 {
		t.Errorf("expected speed 3.3, got %v", speed2)
	}
	if bitrate2 != 4321.0 {
		t.Errorf("expected bitrate 4321.0, got %v", bitrate2)
	}
}

// Minimal fake for exec.Cmd.Process for testing Stop

type fakeProcess struct {
	signalCalled bool
	killCalled   bool
	signalErr    error
}

func (f *fakeProcess) Signal(sig syscall.Signal) error {
	f.signalCalled = true
	return f.signalErr
}

func (f *fakeProcess) Kill() error {
	f.killCalled = true
	return nil
}

func TestFFmpegProcess_Stop_EarlyReturn(t *testing.T) {
	p := &FFmpegProcess{Status: FFmpegStopped}
	err := p.Stop(10 * time.Millisecond)
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestFFmpegProcess_Stop_WaitCh(t *testing.T) {
	proc := &fakeProcess{}
	waitCh := make(chan error, 1)
	waitCh <- nil
	p := &FFmpegProcess{
		Status:  FFmpegRunning,
		Process: proc,
		waitCh:  waitCh,
	}

	err := p.Stop(10 * time.Millisecond)
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if !proc.signalCalled {
		t.Error("expected Signal to be called")
	}
	if proc.killCalled {
		t.Error("did not expect Kill to be called")
	}
}

func TestFFmpegProcess_Stop_Timeout(t *testing.T) {
	proc := &fakeProcess{}
	waitCh := make(chan error)
	p := &FFmpegProcess{
		Status:  FFmpegRunning,
		Process: proc,
		waitCh:  waitCh,
	}

	err := p.Stop(10 * time.Millisecond)
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if !proc.signalCalled {
		t.Error("expected Signal to be called")
	}
	if !proc.killCalled {
		t.Error("expected Kill to be called on timeout")
	}
}

func TestFFmpegProcess_GetLastOutputLines(t *testing.T) {
	p := &FFmpegProcess{}

	// Empty output
	lines := p.GetLastOutputLines(3)
	if lines != nil {
		t.Errorf("expected nil for empty output, got %v", lines)
	}

	// Fewer lines than n
	p.outputBuf.WriteString("a\nb\n")
	lines = p.GetLastOutputLines(3)
	if len(lines) != 2 || lines[0] != "a" || lines[1] != "b" {
		t.Errorf("expected [a b], got %v", lines)
	}

	// Exactly n lines
	p.outputBuf.Reset()
	p.outputBuf.WriteString("1\n2\n3\n")
	lines = p.GetLastOutputLines(3)
	if len(lines) != 3 || lines[0] != "1" || lines[2] != "3" {
		t.Errorf("expected [1 2 3], got %v", lines)
	}

	// More than n lines
	p.outputBuf.Reset()
	p.outputBuf.WriteString("x\ny\nz\nw\n")
	lines = p.GetLastOutputLines(2)
	if len(lines) != 2 || lines[0] != "z" || lines[1] != "w" {
		t.Errorf("expected [z w], got %v", lines)
	}
}
