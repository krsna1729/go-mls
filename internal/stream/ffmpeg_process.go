package stream

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// FFmpegStatus represents the state of an ffmpeg process
const (
	FFmpegStarting = iota
	FFmpegRunning
	FFmpegStopped
	FFmpegError
)

// FFmpegProcess manages a single ffmpeg process and its lifecycle.
//
// Concurrency notes:
// - Fields in the 'immutable' group are set once at construction and never changed.
// - Fields in the 'set-once' group are set at Start() and then read-only.
// - Fields in the 'mutable (protected by mu)' group may be read/written by multiple goroutines and must be accessed with mu held.
// - waitOnce/waitCh are used to ensure only one goroutine calls Wait() on Cmd, and all others wait on the channel.
// - Output capture: FFmpegProcess captures stdout/stderr for both progress parsing and error reporting
type FFmpegProcess struct {
	// --- Immutable after construction ---
	Cmd      *exec.Cmd          // Underlying ffmpeg command (never reassigned)
	Cancel   context.CancelFunc // Context cancel function (never reassigned)
	Ctx      context.Context    // Context for cancellation (never reassigned)
	waitCh   chan error         // Channel for Wait() result (never reassigned)
	waitOnce sync.Once          // Ensures only one Wait() call on Cmd

	// --- Set-once at Start(), then read-only ---
	PID         int       // Set at Start(), then read-only
	StartTime   time.Time // Set at Start(), then read-only
	hasProgress bool      // Whether ffmpeg args include -progress for parsing

	// --- Mutable, protected by mu ---
	Status      int            // FFmpegStarting, FFmpegRunning, etc. (read/written by multiple goroutines)
	Wg          sync.WaitGroup // For external goroutine tracking (if used)
	Speed       float64        // Last parsed speed (e.g., 1.01x)
	LastSpeed   time.Time      // Last time speed was updated
	Bitrate     float64        // Last parsed bitrate (kbps)
	LastBitrate time.Time      // Last time bitrate was updated
	outputBuf   bytes.Buffer   // Captured stdout/stderr for error reporting
	mu          sync.Mutex     // Protects Status and all mutable fields above
}

// NewFFmpegProcess creates a new FFmpegProcess with context and process group
func NewFFmpegProcess(ctx context.Context, args ...string) (*FFmpegProcess, error) {
	c, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(c, "ffmpeg", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Check if args contain -progress for progress parsing
	hasProgress := false
	for i, arg := range args {
		if arg == "-progress" && i+1 < len(args) && strings.Contains(args[i+1], "pipe:") {
			hasProgress = true
			break
		}
	}

	proc := &FFmpegProcess{
		Cmd:         cmd,
		Status:      FFmpegStarting,
		Cancel:      cancel,
		Ctx:         c,
		waitCh:      make(chan error, 1),
		hasProgress: hasProgress,
	}
	return proc, nil
}

// Start launches the ffmpeg process and sets PID/StartTime
func (p *FFmpegProcess) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Only set up pipes if they haven't been set already
	var stdoutPipe, stderrPipe io.ReadCloser
	var err error

	if p.Cmd.Stdout == nil {
		stdoutPipe, err = p.Cmd.StdoutPipe()
		if err != nil {
			p.Status = FFmpegError
			return err
		}
	}

	if p.Cmd.Stderr == nil {
		stderrPipe, err = p.Cmd.StderrPipe()
		if err != nil {
			p.Status = FFmpegError
			return err
		}
	}

	if err := p.Cmd.Start(); err != nil {
		p.Status = FFmpegError
		return err
	}
	p.PID = p.Cmd.Process.Pid
	p.Status = FFmpegRunning
	p.StartTime = time.Now()

	// Start a goroutine to call Wait() exactly once
	go func() {
		p.waitOnce.Do(func() {
			err := p.Cmd.Wait()
			p.waitCh <- err
			close(p.waitCh)
		})
	}()

	// Start goroutines to handle output only if we have pipes
	if stdoutPipe != nil {
		if p.hasProgress {
			// For progress parsing commands, parse stdout for speed/bitrate
			go p.parseProgress(stdoutPipe)
		} else {
			// For non-progress commands, capture stdout
			go p.captureOutput(stdoutPipe)
		}
	}

	if stderrPipe != nil {
		// Always capture stderr for error reporting
		go p.captureOutput(stderrPipe)
	}

	return nil
}

// parseProgress parses ffmpeg -progress output for speed and bitrate
func (p *FFmpegProcess) parseProgress(r io.Reader) {
	if r == nil {
		return // No progress output available
	}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "speed=") {
			val := strings.TrimPrefix(line, "speed=")
			val = strings.TrimSuffix(val, "x")
			val = strings.TrimSpace(val)
			if val != "N/A" && val != "" {
				if speed, err := strconv.ParseFloat(val, 64); err == nil {
					p.mu.Lock()
					p.Speed = speed
					p.LastSpeed = time.Now()
					p.mu.Unlock()
				}
			}
		}
		if strings.HasPrefix(line, "bitrate=") {
			val := strings.TrimPrefix(line, "bitrate=")
			val = strings.TrimSpace(val)
			if strings.HasSuffix(val, "kbits/s") {
				val = strings.TrimSuffix(val, "kbits/s")
				val = strings.TrimSpace(val)
			}
			if val != "N/A" && val != "" {
				if bitrate, err := strconv.ParseFloat(val, 64); err == nil {
					p.mu.Lock()
					p.Bitrate = bitrate
					p.LastBitrate = time.Now()
					p.mu.Unlock()
				}
			}
		}
		select {
		case <-p.Ctx.Done():
			return
		default:
		}
	}
	if err := scanner.Err(); err != nil {
		// Handle scanner error (e.g., log it)
	}
}

// captureOutput captures stdout/stderr output for error reporting
func (p *FFmpegProcess) captureOutput(r io.Reader) {
	if r == nil {
		return // No output available
	}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			p.mu.Lock()
			p.outputBuf.WriteString(line)
			p.outputBuf.WriteString("\n")
			p.mu.Unlock()
		}
		select {
		case <-p.Ctx.Done():
			return
		default:
		}
	}
	if err := scanner.Err(); err != nil {
		// Handle scanner error (e.g., log it)
	}
}

// GetSpeed returns the last parsed speed and time (concurrent-safe)
// Use this from relay managers to get up-to-date ffmpeg speed.
func (p *FFmpegProcess) GetSpeed() (float64, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Speed, p.LastSpeed
}

// GetBitrate returns the last parsed bitrate and time (concurrent-safe)
// Use this from relay managers to get up-to-date ffmpeg bitrate.
func (p *FFmpegProcess) GetBitrate() (float64, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Bitrate, p.LastBitrate
}

// SetStats allows tests or wrappers to inject stats (optional, for extensibility)
func (p *FFmpegProcess) SetStats(speed, bitrate float64) {
	p.mu.Lock()
	p.Speed = speed
	p.Bitrate = bitrate
	p.LastSpeed = time.Now()
	p.LastBitrate = time.Now()
	p.mu.Unlock()
}

// Wait waits for the ffmpeg process to exit (safe for concurrent calls)
func (p *FFmpegProcess) Wait() error {
	return <-p.waitCh
}

// Stop attempts graceful shutdown, then force kills if needed
func (p *FFmpegProcess) Stop(timeout time.Duration) error {
	p.mu.Lock()
	if p.Status != FFmpegRunning || p.Cmd == nil || p.Cmd.Process == nil {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()
	// Use SIGTERM for graceful shutdown (ffmpeg handles SIGTERM cleanly)
	err := p.Cmd.Process.Signal(syscall.SIGTERM)
	if err != nil {
		// Fallback to SIGKILL if SIGTERM fails
		_ = p.Cmd.Process.Kill()
	}
	// Wait for process to exit or timeout
	select {
	case <-time.After(timeout):
		_ = p.Cmd.Process.Kill()
		return nil
	case <-p.waitCh:
		return nil
	}
}

// GetOutput returns the captured output (concurrent-safe)
// Use this to get ffmpeg output for error reporting.
func (p *FFmpegProcess) GetOutput() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.outputBuf.String()
}

// GetLastOutputLines returns the last N lines of captured output (concurrent-safe)
func (p *FFmpegProcess) GetLastOutputLines(n int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	output := p.outputBuf.String()
	if output == "" {
		return nil
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}
