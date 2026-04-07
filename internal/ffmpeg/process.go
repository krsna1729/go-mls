package ffmpeg

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"go-mls/internal/logger"
	"go-mls/internal/state"

	"github.com/shirou/gopsutil/v3/process"
)

type Process interface {
	PID() int
	Stop()
	Wait() error
	Done() <-chan struct{}
	Err() error
}

// stderrTailSize is the number of recent ffmpeg stderr lines retained for
// post-mortem logging when the process exits unexpectedly.
const stderrTailSize = 20

var (
	binaryPathMu sync.RWMutex
	binaryPath   = "ffmpeg"
	logLevel     = "error"
)

func SetBinaryPath(path string) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		trimmed = "ffmpeg"
	}
	binaryPathMu.Lock()
	binaryPath = trimmed
	binaryPathMu.Unlock()
}

func getBinaryPath() string {
	binaryPathMu.RLock()
	defer binaryPathMu.RUnlock()
	return binaryPath
}

func SetLogLevel(level string) {
	trimmed := strings.TrimSpace(level)
	if trimmed == "" {
		trimmed = "error"
	}
	binaryPathMu.Lock()
	logLevel = trimmed
	binaryPathMu.Unlock()
}

func getLogLevel() string {
	binaryPathMu.RLock()
	defer binaryPathMu.RUnlock()
	return logLevel
}

// FFmpegProcess represents a managed FFmpeg child process.
type FFmpegProcess struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	pid    int
	mu     sync.Mutex
	done   chan struct{}
	err    error
	log    *logger.Logger
	store  *state.Store
	stderr io.Closer

	// stderrDone is closed when the parseStderr goroutine exits, ensuring the
	// tail buffer is fully populated before wait() inspects it.
	stderrDone chan struct{}
	// stopped is set to true when Stop() is called so that a non-zero exit
	// code from an intentional shutdown does not trigger error logging.
	stopped bool
	tailMu  sync.Mutex
	tailBuf []string // ring buffer of the last stderrTailSize stderr lines
}

// RunAndMonitor starts an FFmpeg process and continuously monitors it.
// It parses stderr for video telemetry and polls gopsutil for hardware usage.
// The returned FFmpegProcess can be used to stop the process.
func RunAndMonitor(ctx context.Context, store *state.Store, log *logger.Logger, args ...string) (*FFmpegProcess, error) {
	childCtx, cancel := context.WithCancel(ctx)
	effectiveArgs := args
	if !containsLogLevelArg(args) {
		effectiveArgs = append([]string{"-loglevel", getLogLevel()}, args...)
	}
	cmd := exec.CommandContext(childCtx, getBinaryPath(), effectiveArgs...)
	cmd.Stdout = nil // Not used

	// Put ffmpeg in its own process group so SIGTERM doesn't propagate from parent
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	ffmpegLog := log
	if ffmpegLog == nil {
		ffmpegLog = logger.NewLogger()
	}
	ffmpegLog = ffmpegLog.With("component", "ffmpeg", "pid", cmd.Process.Pid)

	fp := &FFmpegProcess{
		cmd:        cmd,
		cancel:     cancel,
		pid:        cmd.Process.Pid,
		done:       make(chan struct{}),
		stderrDone: make(chan struct{}),
		log:        ffmpegLog,
		store:      store,
		stderr:     stderrPipe,
	}

	// Start telemetry goroutines
	go fp.parseStderr(stderrPipe)
	go fp.pollHardware(childCtx)
	go fp.wait()

	return fp, nil
}

// PID returns the process ID.
func (fp *FFmpegProcess) PID() int {
	return fp.pid
}

// Done returns a channel that closes when the process exits.
func (fp *FFmpegProcess) Done() <-chan struct{} {
	return fp.done
}

// Err returns the process exit error, if any.
func (fp *FFmpegProcess) Err() error {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return fp.err
}

// Stop signals the process to stop gracefully.
// If the process doesn't exit within the timeout, it kills the entire process group.
func (fp *FFmpegProcess) Stop() {
	fp.mu.Lock()
	fp.stopped = true
	pid := fp.pid
	stderr := fp.stderr
	fp.mu.Unlock()

	// Send SIGTERM to initiate graceful shutdown
	fp.log.Debug("Sending SIGTERM to ffmpeg", "pid", pid)
	syscall.Kill(pid, syscall.SIGTERM)

	// Close stderr pipe to unblock parseStderr goroutine
	if stderr != nil {
		stderr.Close()
	}

	// Wait a short time for graceful shutdown, then kill process group if needed
	go func() {
		select {
		case <-fp.done:
			return
		case <-time.After(5 * time.Second):
			// Graceful shutdown didn't work, kill the entire process group
			fp.killProcessGroup()
		}
	}()
}

// Wait blocks until the process has fully exited.
func (fp *FFmpegProcess) Wait() error {
	<-fp.done
	if fp.store != nil {
		fp.store.RemoveTelemetry(fp.pid)
	}
	return fp.Err()
}

func (fp *FFmpegProcess) killProcessGroup() {
	fp.mu.Lock()
	defer fp.mu.Unlock()

	if fp.cmd == nil || fp.cmd.Process == nil {
		return
	}

	// Kill the entire process group (negative PID means process group)
	pgid := fp.cmd.Process.Pid
	fp.log.Debug("Killing process group", "pgid", pgid)
	syscall.Kill(-pgid, syscall.SIGKILL)
}

func containsLogLevelArg(args []string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "-loglevel" {
			return true
		}
	}
	return false
}

func (fp *FFmpegProcess) wait() {
	err := fp.cmd.Wait()
	// Wait for parseStderr to drain the pipe fully so the tail buffer is
	// complete before we inspect it.
	<-fp.stderrDone
	fp.mu.Lock()
	fp.err = err
	stopped := fp.stopped
	fp.mu.Unlock()
	if err != nil && !stopped {
		fp.tailMu.Lock()
		tail := make([]string, len(fp.tailBuf))
		copy(tail, fp.tailBuf)
		fp.tailMu.Unlock()
		if len(tail) > 0 {
			fp.log.Error("ffmpeg process failed; last stderr output",
				"exit_err", err,
				"stderr_tail", strings.Join(tail, "\n"))
		}
	}
	if fp.store != nil {
		fp.store.RemoveTelemetry(fp.pid)
	}
	close(fp.done)
}

// Regex patterns for parsing FFmpeg progress output.
var (
	reFrame   = regexp.MustCompile(`frame=\s*(\d+)`)
	reFPS     = regexp.MustCompile(`fps=\s*([\d.]+)`)
	reBitrate = regexp.MustCompile(`bitrate=\s*([\d.]+)kbits/s`)
	reSpeed   = regexp.MustCompile(`speed=\s*([\d.]+)x`)
)

// parseStderr reads FFmpeg stderr using a custom \r scanner for real-time progress.
func (fp *FFmpegProcess) parseStderr(r io.Reader) {
	defer close(fp.stderrDone)
	scanner := bufio.NewScanner(r)
	scanner.Split(scanCRLF) // Custom split function that handles \r

	for scanner.Scan() {
		line := scanner.Text()

		// Always append to the tail ring buffer for post-mortem logging.
		fp.tailMu.Lock()
		fp.tailBuf = append(fp.tailBuf, line)
		if len(fp.tailBuf) > stderrTailSize {
			fp.tailBuf = fp.tailBuf[len(fp.tailBuf)-stderrTailSize:]
		}
		fp.tailMu.Unlock()

		if strings.Contains(line, "frame=") || strings.Contains(line, "speed=") {
			if fp.store == nil {
				continue
			}
			t := parseProgressLine(line)
			if existing, ok := fp.store.GetTelemetry(fp.pid); ok && existing != nil {
				t.CPU = existing.CPU
				t.MemMB = existing.MemMB
			}
			fp.store.UpdateTelemetry(fp.pid, t)
		} else {
			// Log lines that likely indicate problems. The check is
			// case-insensitive to catch ffmpeg's varied capitalisation.
			lower := strings.ToLower(line)
			if strings.Contains(lower, "error") ||
				strings.Contains(lower, "failed") ||
				strings.Contains(lower, "refused") ||
				strings.Contains(lower, "fatal") ||
				strings.Contains(lower, "no such file") {
				fp.log.Error("ffmpeg stderr", "line", line)
			}
		}
	}
}

// parseProgressLine extracts telemetry fields from a single FFmpeg progress line.
func parseProgressLine(line string) *state.Telemetry {
	t := &state.Telemetry{}
	if m := reFrame.FindStringSubmatch(line); len(m) > 1 {
		t.Frame, _ = strconv.ParseInt(m[1], 10, 64)
	}
	if m := reFPS.FindStringSubmatch(line); len(m) > 1 {
		t.FPS, _ = strconv.ParseFloat(m[1], 64)
	}
	if m := reBitrate.FindStringSubmatch(line); len(m) > 1 {
		t.Bitrate, _ = strconv.ParseFloat(m[1], 64)
	}
	if m := reSpeed.FindStringSubmatch(line); len(m) > 1 {
		t.Speed, _ = strconv.ParseFloat(m[1], 64)
	}
	return t
}

// pollHardware polls the PID every 2 seconds for CPU and memory usage.
func (fp *FFmpegProcess) pollHardware(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var proc *process.Process

	for {
		select {
		case <-ctx.Done():
			return
		case <-fp.done:
			return
		case <-ticker.C:
			if fp.store == nil {
				continue
			}
			if proc == nil {
				var err error
				proc, err = process.NewProcess(int32(fp.pid))
				if err != nil {
					continue
				}
			}
			// Get existing telemetry or create new
			t, ok := fp.store.GetTelemetry(fp.pid)
			if !ok {
				t = &state.Telemetry{}
			}
			if cpuPct, err := proc.CPUPercent(); err == nil {
				t.CPU = cpuPct
			} else {
				proc = nil
			}
			if memInfo, err := proc.MemoryInfo(); err == nil {
				t.MemMB = float64(memInfo.RSS) / (1024 * 1024)
			} else {
				proc = nil
			}
			fp.store.UpdateTelemetry(fp.pid, t)
		}
	}
}

// scanCRLF is a bufio.SplitFunc that splits on both \r and \n.
// This handles FFmpeg's progress output that uses \r for in-place updates.
func scanCRLF(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	// Find earliest \r or \n
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			return i + 1, data[:i], nil
		}
		if data[i] == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

type NoopFFmpegProcess struct{}

func (p *NoopFFmpegProcess) PID() int    { return 0 }
func (p *NoopFFmpegProcess) Stop()       {}
func (p *NoopFFmpegProcess) Wait() error { return nil }
func (p *NoopFFmpegProcess) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
func (p *NoopFFmpegProcess) Err() error { return nil }
