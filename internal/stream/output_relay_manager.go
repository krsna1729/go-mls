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
	Proc         FFmpegProcess     // may be replaced on restart, protected by mu
	Status       OutputRelayStatus // protected by mu
	LastError    string            // protected by mu
	shuttingDown bool              // protected by mu
	cleanedUp    bool              // protected by mu, ensures cleanup is only done once
	consumer     Consumer          // The consumer for this relay, set when registered

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
// - Logger and cleanupHandler are set at construction and never changed.
type OutputRelayManager struct {
	Relays             map[string]*OutputRelay            // key: output URL, protected by mu
	mu                 sync.Mutex                         // protects Relays
	Logger             *logger.Logger                     // immutable
	cleanupHandler     ConsumerCleanupHandler             // Consumer pattern cleanup handler
	_testFFmpegFactory func(args ...string) FFmpegProcess // test-only, nil in prod
}

func NewOutputRelayManager(l *logger.Logger) *OutputRelayManager {
	return &OutputRelayManager{
		Relays: make(map[string]*OutputRelay),
		Logger: l,
	}
}

// SetCleanupHandler sets the handler to be called when a consumer fails
// This is used by the Consumer pattern to auto-unregister consumers and manage refcounts
func (orm *OutputRelayManager) SetCleanupHandler(handler ConsumerCleanupHandler) {
	orm.cleanupHandler = handler
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
	var proc FFmpegProcess
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
	err = proc.Start(ctx)
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

// cleanupOutputRelay stops the ffmpeg process, updates relay state, and ensures failure callback is only called once.
// Returns true if failure callback should be called (i.e., not graceful shutdown, not already cleaned up, not already stopped).
func (orm *OutputRelayManager) cleanupOutputRelay(relay *OutputRelay, reason string) (shouldCallFailure bool, inputURL, outputURL string) {
	relay.mu.Lock()
	if relay.cleanedUp {
		relay.mu.Unlock()
		return false, relay.InputURL, relay.OutputURL
	}
	proc := relay.Proc
	shuttingDown := relay.shuttingDown
	inputURL = relay.InputURL
	outputURL = relay.OutputURL
	// Mark as cleaned up to prevent double-callbacks
	relay.cleanedUp = true
	relay.Proc = nil
	relay.Status = OutputStopped
	relay.mu.Unlock()

	// Stop the process outside the lock
	if proc != nil {
		err := proc.Stop(context.Background(), 2*time.Second)
		if err != nil {
			orm.Logger.Warn("Error stopping ffmpeg process during cleanup", "outputURL", outputURL, "err", err, "reason", reason)
		}
	}

	// Only call consumer cleanup if this is NOT a graceful shutdown and we have a consumer
	relay.mu.Lock()
	consumer := relay.consumer
	relay.mu.Unlock()

	if !shuttingDown && consumer != nil && orm.cleanupHandler != nil {
		orm.Logger.Warn("Calling consumer OnFailure for failed output", "inputURL", inputURL, "outputURL", outputURL, "reason", reason)
		consumer.OnFailure(orm.cleanupHandler) // Dependency injection!
	}
	if shuttingDown {
		orm.Logger.Info("Graceful shutdown, not calling consumer cleanup", "outputURL", outputURL, "reason", reason)
	}
	return false, inputURL, outputURL
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
	relay.mu.Unlock()
	orm.mu.Unlock()

	orm.cleanupOutputRelay(relay, "stop")
}

// RunOutputRelay runs and monitors the output relay process
func (orm *OutputRelayManager) RunOutputRelay(relay *OutputRelay) {
	orm.Logger.Info("Running output relay", "localURL", relay.LocalURL, "outputURL", relay.OutputURL)
	var proc FFmpegProcess
	relay.mu.Lock()
	proc = relay.Proc
	relay.mu.Unlock()
	if proc == nil {
		orm.Logger.Error("FFmpegProcess is nil", "outputURL", relay.OutputURL)
		return
	}
	err := proc.Wait()

	relay.mu.Lock()
	shuttingDown := relay.shuttingDown
	outputURL := relay.OutputURL
	alreadyCleaned := relay.cleanedUp
	relay.mu.Unlock()

	if err != nil {
		if !alreadyCleaned {
			orm.cleanupOutputRelay(relay, "run-error")
		}
		if shuttingDown {
			orm.Logger.Info("Output relay stopped (signal)", "outputURL", outputURL, "signal", err)
		} else {
			orm.Logger.Error("Output relay process exited with error", "outputURL", outputURL, "err", err)
		}
		return
	}
	// No error: process exited cleanly
	if !alreadyCleaned {
		orm.cleanupOutputRelay(relay, "run-clean")
	}
	orm.Logger.Info("Output relay stopped cleanly", "outputURL", outputURL)
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
	relay.mu.Unlock()
	// Remove from map before stopping process
	delete(orm.Relays, outputURL)
	orm.mu.Unlock()

	orm.cleanupOutputRelay(relay, "delete")
	orm.Logger.Info("Output relay deleted successfully", "outputURL", outputURL)
	return nil
}

// GetOutputRelay returns an output relay safely (read-only access for testing)
func (orm *OutputRelayManager) GetOutputRelay(outputURL string) (*OutputRelay, bool) {
	orm.mu.Lock()
	defer orm.mu.Unlock()
	relay, exists := orm.Relays[outputURL]
	return relay, exists
}
