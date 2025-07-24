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
	Proc         ffmpegProcess     // may be replaced on restart, protected by mu
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
	Relays             map[string]*OutputRelay            // key: output URL, protected by mu
	mu                 sync.Mutex                         // protects Relays
	Logger             *logger.Logger                     // immutable
	FailureCallback    func(inputURL, outputURL string)   // immutable after set
	_testFFmpegFactory func(args ...string) ffmpegProcess // test-only, nil in prod
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
	orm.Logger.Info("Starting output relay", "inputURL", config.InputURL, "localURL", config.LocalURL, "outputURL", config.OutputURL)
	// Validate config
	if config.OutputURL == "" {
		return fmt.Errorf("OutputURL cannot be empty")
	}
	if config.InputURL == "" {
		return fmt.Errorf("InputURL cannot be empty")
	}
	orm.mu.Lock()
	relay, exists := orm.Relays[config.OutputURL]
	if exists && relay.Status == OutputRunning {
		orm.Logger.Warn("Output relay already running", "localURL", config.LocalURL, "outputURL", config.OutputURL)
		orm.mu.Unlock()
		return fmt.Errorf("output relay already running for %s", config.OutputURL)
	}
	ctx := context.Background() // Use background context for now; can be enhanced for cancellation
	var proc ffmpegProcess
	var err error
	if orm._testFFmpegFactory != nil {
		proc = orm._testFFmpegFactory(config.FFmpegArgs...)
		err = nil
	} else {
		proc, err = NewFFmpegProcess(ctx, append(config.FFmpegArgs, "-progress", "pipe:1")...)
	}
	if err != nil {
		orm.mu.Unlock()
		orm.Logger.Error("Failed to create output relay ffmpeg process", "err", err)
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
		relay.mu.Lock()
		// Set status to OutputError so that restart is allowed
		relay.Status = OutputError
		relay.LastError = err.Error()
		relay.Proc = nil
		relay.mu.Unlock()
		orm.mu.Unlock()
		orm.Logger.Error("Failed to start output relay ffmpeg", "err", err)
		return err
	}
	orm.Logger.Info("Started ffmpeg process", "inputURL", config.InputURL, "localURL", config.LocalURL, "outputURL", config.OutputURL)
	go orm.RunOutputRelay(relay)
	return nil
}

// StopOutputRelay stops an output ffmpeg process
func (orm *OutputRelayManager) StopOutputRelay(outputURL string) {
	orm.Logger.Info("Stopping output relay", "outputURL", outputURL)
	orm.mu.Lock()
	relay, exists := orm.Relays[outputURL]
	if !exists {
		orm.Logger.Warn("relay not found", "outputURL", outputURL)
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
			orm.Logger.Warn("Error stopping ffmpeg process", "outputURL", outputURL, "err", err)
		}
	}
	// Only call failure callback if this is NOT a graceful shutdown
	if !shuttingDown && orm.FailureCallback != nil {
		orm.Logger.Debug("Calling failure callback for failed output", "inputURL", inputURL, "outputURL", outputURL)
		orm.FailureCallback(inputURL, outputURL)
	} else if shuttingDown {
		orm.Logger.Debug("Graceful shutdown, not calling failure callback", "outputURL", outputURL)
	}
}

// RunOutputRelay runs and monitors the output relay process
func (orm *OutputRelayManager) RunOutputRelay(relay *OutputRelay) {
	orm.Logger.Info("Running output relay", "localURL", relay.LocalURL, "outputURL", relay.OutputURL)
	var proc ffmpegProcess
	relay.mu.Lock()
	proc = relay.Proc
	relay.mu.Unlock()
	if proc == nil {
		orm.Logger.Error("FFmpegProcess is nil", "outputURL", relay.OutputURL)
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
			orm.Logger.Info("Output relay stopped (signal)", "outputURL", outputURL, "signal", err)
		} else {
			orm.Logger.Info("Output relay stopped cleanly", "outputURL", outputURL)
		}
		return
	}
	if err != nil {
		orm.Logger.Error("Output relay process exited with error", "outputURL", outputURL, "err", err)
		if !shuttingDown && orm.FailureCallback != nil {
			orm.Logger.Debug("Calling failure callback", "inputURL", inputURL, "outputURL", outputURL)
			orm.FailureCallback(inputURL, outputURL)
			return
		} else {
			orm.Logger.Debug("Output relay exited with error during graceful shutdown, skipping failure callback", "outputURL", outputURL)
		}
	} else {
		orm.Logger.Info("Output relay process completed successfully", "outputURL", outputURL)
	}
}

// DeleteOutput completely removes an output relay
func (orm *OutputRelayManager) DeleteOutput(outputURL string) error {
	orm.Logger.Info("Deleting output", "outputURL", outputURL)
	orm.mu.Lock()
	relay, exists := orm.Relays[outputURL]
	if !exists {
		orm.Logger.Warn("relay not found", "outputURL", outputURL)
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
			orm.Logger.Warn("Error deleting ffmpeg process", "outputURL", outputURL, "err", err)
		}
	}

	// Always call failure callback for deleted outputs to decrement input relay refcount
	if orm.FailureCallback != nil {
		orm.Logger.Debug("Calling failure callback for deleted output", "inputURL", inputURL, "outputURL", outputURL)
		orm.FailureCallback(inputURL, outputURL)
	}
	orm.Logger.Info("Output relay deleted successfully", "outputURL", outputURL)
	return nil
}
