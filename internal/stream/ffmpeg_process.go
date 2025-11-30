package stream

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"go-mls/internal/logger"
)

var log = logger.NewLogger()

// FFmpegStatus represents the state of an ffmpeg process
const (
	FFmpegStarting = iota
	FFmpegRunning
	FFmpegStopped
	FFmpegError
)

// ProcessHandle abstracts process signaling for testability
//go:generate mockgen -destination=mock_processhandle.go -package=stream . ProcessHandle

type ProcessHandle interface {
	Signal(sig syscall.Signal) error
	Kill() error
}

type osProcessHandle struct {
	p *os.Process
}

func (h *osProcessHandle) Signal(sig syscall.Signal) error {
	return h.p.Signal(sig)
}
func (h *osProcessHandle) Kill() error {
	return h.p.Kill()
}

// FFmpegProcess defines the interface for managing an ffmpeg process.
type FFmpegProcess interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context, timeout time.Duration) error
	Wait() error
	GetOutput() string
	GetSpeed() (float64, time.Time)
	GetBitrate() (float64, bool)
	GetPID() int
	OutputChannel() <-chan string
}

// ffmpegProcess implements FFmpegProcess.
type ffmpegProcess struct {
	cmd         *exec.Cmd
	cancel      context.CancelFunc
	ctx         context.Context
	waitCh      chan error
	waitOnce    sync.Once
	pid         int
	startTime   time.Time
	hasProgress bool

	status      int
	speed       float64
	lastSpeed   time.Time
	bitrate     float64
	lastBitrate time.Time
	outputBuf   bytes.Buffer
	outputCh    chan string
	mu          sync.Mutex
	process     ProcessHandle
}

// NewFFmpegProcess creates a new ffmpegProcess instance.
func NewFFmpegProcess(ctx context.Context, args ...string) (FFmpegProcess, error) {
	cancelCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cancelCtx, "ffmpeg", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	hasProgress := false
	for i, arg := range args {
		if arg == "-progress" && i+1 < len(args) && strings.Contains(args[i+1], "pipe:") {
			hasProgress = true
			break
		}
	}

	proc := &ffmpegProcess{
		cmd:         cmd,
		status:      FFmpegStarting,
		cancel:      cancel,
		ctx:         cancelCtx,
		waitCh:      make(chan error, 1),
		hasProgress: hasProgress,
		outputCh:    make(chan string, 100), // Buffered for output streaming
	}
	return proc, nil
}

// Start launches the ffmpeg process.
func (p *ffmpegProcess) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.status != FFmpegStarting {
		return nil // Already started or stopped
	}

	var stdoutPipe, stderrPipe io.ReadCloser
	var err error

	if p.cmd.Stdout == nil {
		stdoutPipe, err = p.cmd.StdoutPipe()
		if err != nil {
			p.status = FFmpegError
			log.Error("Failed to get ffmpeg stdout pipe", "error", err)
			return err
		}
	}

	if p.cmd.Stderr == nil {
		stderrPipe, err = p.cmd.StderrPipe()
		if err != nil {
			p.status = FFmpegError
			log.Error("Failed to get ffmpeg stderr pipe", "error", err)
			return err
		}
	}

	if err := p.cmd.Start(); err != nil {
		p.status = FFmpegError
		log.Error("Failed to start ffmpeg process", "error", err)
		return err
	}
	p.pid = p.cmd.Process.Pid
	p.status = FFmpegRunning
	p.startTime = time.Now()
	p.process = &osProcessHandle{p: p.cmd.Process}
	log.Info("Started ffmpeg process", "pid", p.pid, "args", p.cmd.Args)

	go func() {
		p.waitOnce.Do(func() {
			err := p.cmd.Wait()
			p.waitCh <- err
			close(p.waitCh)
			log.Info("ffmpeg process exited", "pid", p.pid, "error", err)
		})
	}()

	if stdoutPipe != nil {
		if p.hasProgress {
			go p.parseProgress(stdoutPipe)
		} else {
			go p.captureOutput(stdoutPipe)
		}
	}
	if stderrPipe != nil {
		go p.captureOutput(stderrPipe)
	}

	return nil
}

// parseProgress parses ffmpeg -progress output for speed and bitrate.
func (p *ffmpegProcess) parseProgress(r io.Reader) {
	if r == nil {
		return
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
					p.speed = speed
					p.lastSpeed = time.Now()
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
					p.bitrate = bitrate
					p.lastBitrate = time.Now()
					p.mu.Unlock()
				}
			}
		}
		select {
		case <-p.ctx.Done():
			return
		default:
		}
	}
	if err := scanner.Err(); err != nil {
		log.Warn("ffmpeg progress scanner error", "error", err)
	}
}

// captureOutput streams output lines to outputCh and captures for reporting.
func (p *ffmpegProcess) captureOutput(r io.Reader) {
	if r == nil {
		return
	}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			p.mu.Lock()
			p.outputBuf.WriteString(line)
			p.outputBuf.WriteString("\n")
			p.mu.Unlock()
			select {
			case p.outputCh <- line:
			default:
			}
		}
		select {
		case <-p.ctx.Done():
			return
		default:
		}
	}
	if err := scanner.Err(); err != nil {
		log.Warn("ffmpeg output scanner error", "error", err)
	}
}

// OutputChannel returns a read-only channel for real-time output lines.
func (p *ffmpegProcess) OutputChannel() <-chan string {
	return p.outputCh
}

// GetSpeed returns the last parsed speed and time.
func (p *ffmpegProcess) GetSpeed() (float64, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.speed, p.lastSpeed
}

// GetPID returns the process PID.
func (p *ffmpegProcess) GetPID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pid
}

// GetBitrate returns the last parsed bitrate (kbps) and true if available.
func (p *ffmpegProcess) GetBitrate() (float64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bitrate > 0 {
		return p.bitrate, true
	}
	return 0, false
}

// Wait waits for the ffmpeg process to exit.
func (p *ffmpegProcess) Wait() error {
	return <-p.waitCh
}

// Stop attempts graceful shutdown, then force kills if needed.
func (p *ffmpegProcess) Stop(ctx context.Context, timeout time.Duration) error {
	p.mu.Lock()
	if p.status != FFmpegRunning || p.process == nil {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()
	log.Info("Stopping ffmpeg process", "pid", p.pid)
	err := p.process.Signal(syscall.SIGTERM)
	if err != nil {
		log.Warn("SIGTERM failed, sending SIGKILL", "pid", p.pid, "error", err)
		_ = p.process.Kill()
	}
	select {
	case <-time.After(timeout):
		log.Warn("ffmpeg process did not exit in time, killing", "pid", p.pid)
		_ = p.process.Kill()
		return nil
	case <-p.waitCh:
		log.Info("ffmpeg process stopped", "pid", p.pid)
		return nil
	case <-ctx.Done():
		log.Warn("Stop context cancelled", "pid", p.pid)
		return ctx.Err()
	}
}

// GetOutput returns the captured output.
func (p *ffmpegProcess) GetOutput() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.outputBuf.String()
}

// GetLastOutputLines returns the last N lines of captured output (concurrent-safe)
func (p *ffmpegProcess) GetLastOutputLines(n int) []string {
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
