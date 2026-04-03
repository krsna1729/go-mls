package stream

import (
	"bufio"
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
	// Store last N lines of output to avoid unbounded memory growth
	outputLines      []string
	outputLinesLimit int
	outputLinesIndex int
	outputLinesCount int
	outputCh         chan string
	mu               sync.Mutex
	process          ProcessHandle
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
		// keep last 1000 lines by default
		outputLinesLimit: 1000,
		outputLines:      make([]string, 0, 1000),
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
	// Use a safe buffered reader and limit per-line accumulation to avoid
	// bufio.Scanner "token too long" panics when ffmpeg emits very long lines.
	br := bufio.NewReader(r)
	const maxLineLen = 256 * 1024 // 256 KB max per-line
	var lineBuf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := br.Read(tmp)
		if n > 0 {
			data := tmp[:n]
			for _, b := range data {
				if b == '\n' {
					line := string(lineBuf)
					// process the completed line
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
					lineBuf = lineBuf[:0]
				} else {
					if len(lineBuf) < maxLineLen {
						lineBuf = append(lineBuf, b)
					} else if len(lineBuf) == maxLineLen {
						// mark as truncated and keep discarding until newline
						lineBuf = append(lineBuf, []byte("...(truncated)")...)
					}
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				if len(lineBuf) > 0 {
					// process final line without newline
					line := string(lineBuf)
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
				}
				return
			}
			log.Warn("ffmpeg progress read error", "error", err)
			return
		}
		select {
		case <-p.ctx.Done():
			return
		default:
		}
	}
}

// captureOutput streams output lines to outputCh and captures for reporting.
func (p *ffmpegProcess) captureOutput(r io.Reader) {
	if r == nil {
		return
	}
	// Read in chunks and assemble lines up to a configurable per-line limit
	br := bufio.NewReader(r)
	const maxLineLen = 256 * 1024 // 256 KB
	var lineBuf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := br.Read(tmp)
		if n > 0 {
			data := tmp[:n]
			for _, b := range data {
				if b == '\n' {
					if len(lineBuf) > 0 {
						line := string(lineBuf)
						p.mu.Lock()
						if p.outputLinesCount < p.outputLinesLimit {
							p.outputLines = append(p.outputLines, line)
							p.outputLinesCount++
						} else {
							p.outputLines[p.outputLinesIndex] = line
							p.outputLinesIndex = (p.outputLinesIndex + 1) % p.outputLinesLimit
						}
						p.mu.Unlock()
						select {
						case p.outputCh <- line:
						default:
						}
					}
					lineBuf = lineBuf[:0]
				} else {
					if len(lineBuf) < maxLineLen {
						lineBuf = append(lineBuf, b)
					} else if len(lineBuf) == maxLineLen {
						// mark truncated and keep discarding until newline
						lineBuf = append(lineBuf, []byte("...(truncated)")...)
					}
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				if len(lineBuf) > 0 {
					line := string(lineBuf)
					p.mu.Lock()
					if p.outputLinesCount < p.outputLinesLimit {
						p.outputLines = append(p.outputLines, line)
						p.outputLinesCount++
					} else {
						p.outputLines[p.outputLinesIndex] = line
						p.outputLinesIndex = (p.outputLinesIndex + 1) % p.outputLinesLimit
					}
					p.mu.Unlock()
					select {
					case p.outputCh <- line:
					default:
					}
				}
				return
			}
			log.Warn("ffmpeg output read error", "error", err)
			return
		}
		select {
		case <-p.ctx.Done():
			return
		default:
		}
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
	go func() {
		<-time.After(timeout)
		p.mu.Lock()
		if p.process != nil {
			log.Warn("ffmpeg process did not exit in time, killing", "pid", p.pid)
			_ = p.process.Kill()
		}
		p.mu.Unlock()
	}()
	return nil
}

// GetOutput returns the captured output.
func (p *ffmpegProcess) GetOutput() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.outputLinesCount == 0 {
		return ""
	}
	var b strings.Builder
	// start index: if buffer not full, start at 0; if full, start at outputLinesIndex
	start := 0
	if p.outputLinesCount == p.outputLinesLimit {
		start = p.outputLinesIndex
	}
	for i := 0; i < p.outputLinesCount; i++ {
		idx := (start + i) % p.outputLinesLimit
		b.WriteString(p.outputLines[idx])
		if i < p.outputLinesCount-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// GetLastOutputLines returns the last N lines of captured output (concurrent-safe)
func (p *ffmpegProcess) GetLastOutputLines(n int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.outputLinesCount == 0 {
		return nil
	}
	if n <= 0 {
		return nil
	}
	if n > p.outputLinesCount {
		n = p.outputLinesCount
	}
	res := make([]string, n)
	// compute start of last n lines
	// oldest index is start = (outputLinesIndex - outputLinesCount + outputLinesLimit) % outputLinesLimit if full
	// simpler: iterate from end
	for i := 0; i < n; i++ {
		// position from the newest backwards
		pos := (p.outputLinesIndex - 1 - i + p.outputLinesLimit) % p.outputLinesLimit
		// when buffer not full, outputLinesIndex equals count
		if p.outputLinesCount < p.outputLinesLimit {
			pos = p.outputLinesCount - 1 - i
		}
		res[n-1-i] = p.outputLines[pos]
	}
	return res
}
