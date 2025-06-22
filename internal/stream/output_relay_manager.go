package stream

import (
	"bufio"
	"context"
	"fmt"
	"go-mls/internal/logger"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OutputRelayStatus represents the state of an output relay process
// (local RTSP server -> output URL)
type OutputRelayStatus int

const (
	OutputStarting OutputRelayStatus = iota
	OutputRunning
	OutputStopped
	OutputError
)

// OutputRelay represents a single output ffmpeg process
// and its state
type OutputRelay struct {
	OutputURL      string
	OutputName     string
	InputURL       string // Reference to input URL
	LocalURL       string // Local RTSP server URL
	Cmd            *exec.Cmd
	Status         OutputRelayStatus
	LastError      string
	StartTime      time.Time
	Timeout        time.Duration
	PlatformPreset string            // Store platform preset
	FFmpegOptions  map[string]string // Store ffmpeg options
	FFmpegArgs     []string          // Store ffmpeg arguments
	Bitrate        float64           // Current bitrate in kbps
	LastBitrate    time.Time         // Last time bitrate was updated
	mu             sync.Mutex
	ctx            context.Context    // Context for cancellation
	cancel         context.CancelFunc // Cancel function for graceful shutdown
	wg             sync.WaitGroup     // Wait group for goroutines
}

// OutputRelayConfig contains the configuration for starting an output relay
type OutputRelayConfig struct {
	OutputURL      string
	OutputName     string
	InputURL       string
	LocalURL       string
	Timeout        time.Duration
	PlatformPreset string
	FFmpegOptions  map[string]string
	FFmpegArgs     []string
}

// OutputRelayManager manages all output relays
// (local RTSP server -> output URL)
type OutputRelayManager struct {
	Relays          map[string]*OutputRelay // key: output URL
	mu              sync.Mutex
	Logger          *logger.Logger
	FailureCallback func(inputURL, outputURL string) // Called when output relay fails
}

func NewOutputRelayManager(l *logger.Logger) *OutputRelayManager {
	return &OutputRelayManager{
		Relays: make(map[string]*OutputRelay),
		Logger: l,
	}
}

// SetFailureCallback sets the callback function to be called when an output relay fails
func (orm *OutputRelayManager) SetFailureCallback(callback func(inputURL, outputURL string)) {
	orm.FailureCallback = callback
}

// StartOutputRelay starts an output ffmpeg process from local RTSP to output URL
func (orm *OutputRelayManager) StartOutputRelay(config OutputRelayConfig) error {
	orm.Logger.Info("OutputRelayManager: StartOutputRelay: inputURL=%s, localURL=%s, outputURL=%s", config.InputURL, config.LocalURL, config.OutputURL)
	orm.mu.Lock()
	relay, exists := orm.Relays[config.OutputURL]
	if exists && relay.Status == OutputRunning {
		orm.Logger.Warn("Output relay already running for %s -> %s", config.LocalURL, config.OutputURL)
		orm.mu.Unlock()
		return nil
	}
	cmd := exec.Command("ffmpeg", append(config.FFmpegArgs, "-progress", "pipe:1")...)
	ctx, cancel := context.WithCancel(context.Background())
	relay = &OutputRelay{
		OutputURL:      config.OutputURL,
		OutputName:     config.OutputName,
		InputURL:       config.InputURL,
		LocalURL:       config.LocalURL,
		Cmd:            cmd,
		Status:         OutputRunning,
		StartTime:      time.Now(),
		Timeout:        config.Timeout,
		PlatformPreset: config.PlatformPreset,
		FFmpegOptions:  config.FFmpegOptions,
		FFmpegArgs:     config.FFmpegArgs,
		ctx:            ctx,
		cancel:         cancel,
	}
	orm.Relays[config.OutputURL] = relay
	orm.mu.Unlock()

	// Set up stdout pipe for progress monitoring (prefer progress over stderr)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		orm.mu.Lock()
		relay.Status = OutputError
		relay.LastError = err.Error()
		orm.mu.Unlock()
		orm.Logger.Error("Failed to create stdout pipe for output relay: %v", err)
		return err
	}

	// Log the ffmpeg command and start it
	orm.Logger.Debug("OutputRelayManager: Starting ffmpeg command: %v", cmd.Args)
	if err := cmd.Start(); err != nil {
		orm.mu.Lock()
		relay.Status = OutputError
		relay.LastError = err.Error()
		orm.mu.Unlock()
		orm.Logger.Error("Failed to start output relay ffmpeg: %v", err)
		return err
	}
	orm.Logger.Info("OutputRelayManager: Started ffmpeg process PID %d for %s -> %s", cmd.Process.Pid, config.LocalURL, config.OutputURL)

	// Start bitrate monitoring with context
	relay.wg.Add(1)
	go func() {
		defer relay.wg.Done()
		orm.monitorFFmpegProgress(relay, stdoutPipe)
	}()

	relay.wg.Add(1)
	go func() {
		defer relay.wg.Done()
		orm.RunOutputRelay(relay)
	}()
	return nil
}

// StopOutputRelay stops an output ffmpeg process
func (orm *OutputRelayManager) StopOutputRelay(outputURL string) {
	orm.Logger.Info("OutputRelayManager: StopOutputRelay: outputURL=%s", outputURL)
	orm.mu.Lock()
	relay, exists := orm.Relays[outputURL]
	if !exists {
		orm.Logger.Warn("OutputRelayManager: relay for %s not found", outputURL)
		orm.mu.Unlock()
		return
	}
	relay.mu.Lock()
	if relay.Cmd != nil && relay.Cmd.Process != nil {
		pid := relay.Cmd.Process.Pid
		orm.Logger.Info("OutputRelayManager: Gracefully terminating ffmpeg process PID %d for %s", pid, outputURL)
		// Try SIGTERM first for graceful shutdown
		err := relay.Cmd.Process.Signal(os.Interrupt)
		if err != nil {
			orm.Logger.Warn("Failed to send SIGTERM to output relay process PID %d: %v", pid, err)
			// Fallback to SIGKILL
			err = relay.Cmd.Process.Kill()
			if err != nil {
				orm.Logger.Error("Failed to kill output relay process PID %d: %v", pid, err)
			}
		}
	}

	// Cancel context to stop monitoring goroutines
	if relay.cancel != nil {
		relay.cancel()
	}

	relay.Status = OutputStopped
	relay.Cmd = nil

	// Wait for all goroutines to finish before continuing
	relay.mu.Unlock()
	orm.Logger.Debug("OutputRelayManager: Waiting for goroutines to finish for %s", outputURL)
	relay.wg.Wait()

	orm.mu.Unlock()
}

// RunOutputRelay runs and monitors the output relay process
func (orm *OutputRelayManager) RunOutputRelay(relay *OutputRelay) {
	orm.Logger.Info("OutputRelayManager: RunOutputRelay: running ffmpeg for %s -> %s", relay.LocalURL, relay.OutputURL)
	err := relay.Cmd.Wait()
	relay.mu.Lock()
	pid := "unknown"
	if relay.Cmd != nil && relay.Cmd.Process != nil {
		pid = fmt.Sprintf("%d", relay.Cmd.Process.Pid)
	}
	if relay.Status == OutputStopped {
		if err != nil {
			orm.Logger.Info("Output relay PID %s for %s stopped (signal: %v)", pid, relay.OutputURL, err)
		} else {
			orm.Logger.Info("Output relay PID %s for %s stopped cleanly", pid, relay.OutputURL)
		}
		relay.mu.Unlock()
		return
	}
	if err != nil {
		relay.Status = OutputError
		relay.LastError = err.Error()
		orm.Logger.Error("Output relay process PID %s exited with error for %s: %v", pid, relay.OutputURL, err)

		// Call failure callback to clean up input relay refcount
		inputURL := relay.InputURL
		outputURL := relay.OutputURL
		relay.mu.Unlock()
		if orm.FailureCallback != nil {
			orm.Logger.Debug("OutputRelayManager: Calling failure callback for inputURL=%s, outputURL=%s", inputURL, outputURL)
			orm.FailureCallback(inputURL, outputURL)
		}
		return
	} else {
		relay.Status = OutputStopped
		orm.Logger.Info("Output relay process PID %s for %s completed successfully", pid, relay.OutputURL)
	}
	relay.Cmd = nil
	relay.mu.Unlock()
}

// monitorFFmpegProgress monitors ffmpeg -progress output for bitrate information
func (orm *OutputRelayManager) monitorFFmpegProgress(relay *OutputRelay, progressReader io.Reader) {
	scanner := bufio.NewScanner(progressReader)
	for scanner.Scan() {
		// Check if context was cancelled
		select {
		case <-relay.ctx.Done():
			orm.Logger.Debug("OutputRelay progress monitoring cancelled for %s", relay.OutputURL)
			return
		default:
		}

		line := scanner.Text()
		// orm.Logger.Debug("OutputRelay ffmpeg progress line: %s", line)
		if strings.HasPrefix(line, "bitrate=") {
			val := strings.TrimPrefix(line, "bitrate=")
			val = strings.TrimSpace(val)
			// ffmpeg emits e.g. "1621.2kbits/s"; strip suffix for parsing
			if strings.HasSuffix(val, "kbits/s") {
				val = strings.TrimSuffix(val, "kbits/s")
				val = strings.TrimSpace(val)
			}
			if val == "N/A" || val == "" {
				orm.Logger.Debug("OutputRelay bitrate N/A for %s", relay.OutputName)
				continue // Do not update bitrate if N/A
			}
			if bitrate, err := strconv.ParseFloat(val, 64); err == nil {
				relay.mu.Lock()
				relay.Bitrate = bitrate
				relay.LastBitrate = time.Now()
				relay.mu.Unlock()
				orm.Logger.Debug("OutputRelay bitrate updated: %s -> %.2f kbps", relay.OutputName, bitrate)
			} else {
				orm.Logger.Warn("Failed to parse bitrate from ffmpeg progress: '%s' (err: %v)", val, err)
			}
		}
	}
}

// DeleteOutput completely removes an output relay
func (orm *OutputRelayManager) DeleteOutput(outputURL string) error {
	orm.Logger.Info("OutputRelayManager: DeleteOutput: outputURL=%s", outputURL)
	orm.mu.Lock()
	relay, exists := orm.Relays[outputURL]
	if !exists {
		orm.Logger.Warn("OutputRelayManager: relay for %s not found", outputURL)
		orm.mu.Unlock()
		return fmt.Errorf("output relay not found: %s", outputURL)
	}
	relay.mu.Lock()

	// Force stop the relay process
	if relay.Cmd != nil && relay.Cmd.Process != nil {
		pid := relay.Cmd.Process.Pid
		orm.Logger.Info("OutputRelayManager: Force terminating ffmpeg process PID %d for %s", pid, outputURL)
		// Try SIGTERM first for graceful shutdown
		err := relay.Cmd.Process.Signal(os.Interrupt)
		if err != nil {
			orm.Logger.Warn("Failed to send SIGTERM to output relay process PID %d: %v", pid, err)
			// Fallback to SIGKILL
			err = relay.Cmd.Process.Kill()
			if err != nil {
				orm.Logger.Error("Failed to kill output relay process PID %d: %v", pid, err)
			}
		}
	}

	// Cancel context to stop monitoring goroutines
	if relay.cancel != nil {
		relay.cancel()
	}

	relay.Status = OutputStopped
	relay.Cmd = nil
	inputURL := relay.InputURL // Store for callback

	// Wait for all goroutines to finish
	relay.mu.Unlock()
	orm.Logger.Debug("OutputRelayManager: Waiting for goroutines to finish for %s", outputURL)
	relay.wg.Wait()

	// Remove from map
	delete(orm.Relays, outputURL)
	orm.mu.Unlock()

	// Call failure callback to clean up input relay refcount
	if orm.FailureCallback != nil {
		orm.Logger.Debug("OutputRelayManager: Calling failure callback for deleted output inputURL=%s, outputURL=%s", inputURL, outputURL)
		orm.FailureCallback(inputURL, outputURL)
	}

	orm.Logger.Info("OutputRelayManager: Output relay %s deleted successfully", outputURL)
	return nil
}
