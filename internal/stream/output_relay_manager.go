package stream

import (
	"context"
	"fmt"
	"go-mls/internal/logger"
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

// OutputRelay represents a single output ffmpeg process and its state.
//
// Concurrency notes:
// - Immutable fields are set at construction and never changed.
// - Set-once fields are set at Start and then read-only.
// - Mutable fields must be accessed with mu held.
type OutputRelay struct {
	// --- Immutable after construction ---
	OutputURL  string // never changes
	OutputName string // never changes
	InputURL   string // never changes

	// --- Set-once at Start, then read-only ---
	LocalURL       string            // set at Start, then read-only
	Timeout        time.Duration     // set at Start, then read-only
	PlatformPreset string            // set at Start, then read-only
	FFmpegOptions  map[string]string // set at Start, then read-only
	FFmpegArgs     []string          // set at Start, then read-only

	// --- Mutable, protected by mu ---
	Proc         *FFmpegProcess    // may be replaced on restart, protected by mu
	Status       OutputRelayStatus // protected by mu
	LastError    string            // protected by mu
	shuttingDown bool              // protected by mu

	// --- Concurrency primitives ---
	mu sync.Mutex // protects all mutable fields above
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
//
// Concurrency notes:
// - All accesses to Relays map must hold mu.
// - Logger and FailureCallback are set at construction and never changed.
type OutputRelayManager struct {
	Relays          map[string]*OutputRelay          // key: output URL, protected by mu
	mu              sync.Mutex                       // protects Relays
	Logger          *logger.Logger                   // immutable
	FailureCallback func(inputURL, outputURL string) // immutable after set
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
	ctx := context.Background() // Use background context for now; can be enhanced for cancellation
	proc, err := NewFFmpegProcess(ctx, append(config.FFmpegArgs, "-progress", "pipe:1")...)
	if err != nil {
		orm.mu.Unlock()
		orm.Logger.Error("Failed to create output relay ffmpeg process: %v", err)
		return err
	}
	relay = &OutputRelay{
		OutputURL:      config.OutputURL,
		OutputName:     config.OutputName,
		InputURL:       config.InputURL,
		LocalURL:       config.LocalURL,
		Proc:           proc,
		Status:         OutputRunning,
		Timeout:        config.Timeout,
		PlatformPreset: config.PlatformPreset,
		FFmpegOptions:  config.FFmpegOptions,
		FFmpegArgs:     config.FFmpegArgs,
	}
	orm.Relays[config.OutputURL] = relay
	orm.mu.Unlock()
	// Start ffmpeg process
	err = proc.Start()
	if err != nil {
		orm.mu.Lock()
		relay.Status = OutputError
		relay.LastError = err.Error()
		orm.mu.Unlock()
		orm.Logger.Error("Failed to start output relay ffmpeg: %v", err)
		return err
	}
	orm.Logger.Info("OutputRelayManager: Started ffmpeg process PID %d for %s -> %s", proc.PID, config.LocalURL, config.OutputURL)
	// Start process wait/monitor goroutine
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
	relay.shuttingDown = true
	proc := relay.Proc
	relay.Proc = nil
	relay.Status = OutputStopped
	inputURL := relay.InputURL
	shuttingDown := relay.shuttingDown
	relay.mu.Unlock()
	orm.mu.Unlock()

	// Stop the process outside of any locks
	if proc != nil {
		err := proc.Stop(2 * time.Second)
		if err != nil {
			orm.Logger.Warn("OutputRelayManager: Error stopping ffmpeg process for %s: %v", outputURL, err)
		}
	}
	// Only call failure callback if this is NOT a graceful shutdown
	if !shuttingDown && orm.FailureCallback != nil {
		orm.Logger.Debug("OutputRelayManager: Calling failure callback for failed output inputURL=%s, outputURL=%s", inputURL, outputURL)
		orm.FailureCallback(inputURL, outputURL)
	} else if shuttingDown {
		orm.Logger.Debug("OutputRelayManager: Graceful shutdown for %s, not calling failure callback", outputURL)
	}
}

// RunOutputRelay runs and monitors the output relay process
func (orm *OutputRelayManager) RunOutputRelay(relay *OutputRelay) {
	orm.Logger.Info("OutputRelayManager: RunOutputRelay: running ffmpeg for %s -> %s", relay.LocalURL, relay.OutputURL)
	var proc *FFmpegProcess
	relay.mu.Lock()
	proc = relay.Proc
	relay.mu.Unlock()
	if proc == nil {
		orm.Logger.Error("OutputRelayManager: RunOutputRelay: FFmpegProcess is nil for %s", relay.OutputURL)
		return
	}
	err := proc.Wait()

	relay.mu.Lock()
	status := relay.Status
	shuttingDown := relay.shuttingDown
	inputURL := relay.InputURL
	outputURL := relay.OutputURL
	if err != nil {
		if shuttingDown {
			relay.Status = OutputStopped
			relay.LastError = ""
		} else {
			relay.Status = OutputError
			relay.LastError = err.Error()
		}
	}
	if err == nil {
		relay.Status = OutputStopped
	}
	relay.Proc = nil
	relay.mu.Unlock()

	if status == OutputStopped {
		if err != nil {
			orm.Logger.Info("Output relay for %s stopped (signal: %v)", outputURL, err)
		} else {
			orm.Logger.Info("Output relay for %s stopped cleanly", outputURL)
		}
		return
	}
	if err != nil {
		orm.Logger.Error("Output relay process exited with error for %s: %v", outputURL, err)
		if !shuttingDown && orm.FailureCallback != nil {
			orm.Logger.Debug("OutputRelayManager: Calling failure callback for inputURL=%s, outputURL=%s", inputURL, outputURL)
			orm.FailureCallback(inputURL, outputURL)
			return
		} else {
			orm.Logger.Debug("Output relay exited with error during graceful shutdown for %s, skipping failure callback", outputURL)
		}
	} else {
		orm.Logger.Info("Output relay process for %s completed successfully", outputURL)
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
	relay.shuttingDown = true
	proc := relay.Proc
	relay.Proc = nil
	relay.Status = OutputStopped
	inputURL := relay.InputURL
	relay.mu.Unlock()
	// Remove from map before stopping process
	delete(orm.Relays, outputURL)
	orm.mu.Unlock()

	// Stop the process outside of any locks
	if proc != nil {
		err := proc.Stop(1 * time.Second)
		if err != nil {
			orm.Logger.Warn("OutputRelayManager: Error deleting ffmpeg process for %s: %v", outputURL, err)
		}
	}

	// Always call failure callback for deleted outputs to decrement input relay refcount
	if orm.FailureCallback != nil {
		orm.Logger.Debug("OutputRelayManager: Calling failure callback for deleted output inputURL=%s, outputURL=%s", inputURL, outputURL)
		orm.FailureCallback(inputURL, outputURL)
	}
	orm.Logger.Info("OutputRelayManager: Output relay %s deleted successfully", outputURL)
	return nil
}
