// Package worker implements the runAndMonitorFFmpeg wrapper and telemetry parsing.
package worker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
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
}

// RunAndMonitorFFmpeg starts an FFmpeg process and continuously monitors it.
// It parses stderr for video telemetry and polls gopsutil for hardware usage.
// The returned FFmpegProcess can be used to stop the process.
func RunAndMonitorFFmpeg(ctx context.Context, store *state.Store, log *logger.Logger, args ...string) (*FFmpegProcess, error) {
	childCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(childCtx, "ffmpeg", args...)
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

	fp := &FFmpegProcess{
		cmd:    cmd,
		cancel: cancel,
		pid:    cmd.Process.Pid,
		done:   make(chan struct{}),
		log:    log.With("component", "ffmpeg", "pid", cmd.Process.Pid),
		store:  store,
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
	fp.cancel()
	fp.log.Debug("Stop signal sent to ffmpeg")

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

// killProcessGroup kills the entire process group to ensure ffmpeg and all
// child processes are terminated.
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

// Wait blocks until the process has fully exited.
func (fp *FFmpegProcess) Wait() error {
	<-fp.done
	fp.store.RemoveTelemetry(fp.pid)
	return fp.Err()
}

func (fp *FFmpegProcess) wait() {
	err := fp.cmd.Wait()
	fp.mu.Lock()
	fp.err = err
	fp.mu.Unlock()
	fp.store.RemoveTelemetry(fp.pid)
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
	scanner := bufio.NewScanner(r)
	scanner.Split(scanCRLF) // Custom split function that handles \r

	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "frame=") || strings.Contains(line, "speed=") {
			t := parseProgressLine(line)
			fp.store.UpdateTelemetry(fp.pid, t)
		} else if strings.Contains(line, "Error") || strings.Contains(line, "error") {
			fp.log.Error("ffmpeg stderr", "line", line)
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

	for {
		select {
		case <-ctx.Done():
			return
		case <-fp.done:
			return
		case <-ticker.C:
			proc, err := process.NewProcess(int32(fp.pid))
			if err != nil {
				continue
			}
			// Get existing telemetry or create new
			t, ok := fp.store.GetTelemetry(fp.pid)
			if !ok {
				t = &state.Telemetry{}
			}
			if cpuPct, err := proc.CPUPercent(); err == nil {
				t.CPU = cpuPct
			}
			if memInfo, err := proc.MemoryInfo(); err == nil {
				t.MemMB = float64(memInfo.RSS) / (1024 * 1024)
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
			// If \r\n, treat as one newline
			if i+1 < len(data) && data[i+1] == '\n' {
				return i + 2, data[:i], nil
			}
			return i + 1, data[:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// Ensure stderr is accessible (for tests that need to capture it).
func init() {
	// Prevent FFmpeg from inheriting our stdin
	_ = os.Stdin
}
