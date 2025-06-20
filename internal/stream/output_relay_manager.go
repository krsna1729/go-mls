package stream

import (
	"fmt"
	"go-mls/internal/logger"
	"os"
	"os/exec"
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
	mu             sync.Mutex
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
	Relays        map[string]*OutputRelay // key: output URL
	mu            sync.Mutex
	Logger        *logger.Logger
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
	cmd := exec.Command("ffmpeg", config.FFmpegArgs...)
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
	}
	orm.Relays[config.OutputURL] = relay
	orm.mu.Unlock()
	
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
	
	go orm.RunOutputRelay(relay)
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
	relay.Status = OutputStopped
	relay.Cmd = nil
	relay.mu.Unlock()
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
