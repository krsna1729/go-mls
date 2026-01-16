package stream

import (
	"context"
	"fmt"
	"go-mls/internal/logger"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// InputConfig stores persistent input configuration
type InputConfig struct {
	InputURL  string `json:"input_url"`
	InputName string `json:"input_name"`
}

// InputRelayStatus represents the state of an input relay process
// (input URL -> local RTSP server)
type InputRelayStatus int

const (
	InputStarting InputRelayStatus = iota
	InputRunning
	InputStopped
	InputError
)

// InputRelay represents a single input ffmpeg process and its state.
//
// Concurrency notes:
// - Immutable fields are set at construction and never changed.
// - Set-once fields are set at Start and then read-only.
// - Mutable fields must be accessed with mu held.
type InputRelay struct {
	// --- Immutable after construction ---
	InputURL  string // never changes
	InputName string // never changes

	// --- Set-once at Start, then read-only ---
	LocalURL string        // set at Start, then read-only
	Timeout  time.Duration // set at Start, then read-only

	// --- Mutable, protected by mu ---
	Proc      FFmpegProcess    // may be replaced on restart, protected by mu
	Status    InputRelayStatus // read/written by multiple goroutines, protected by mu
	LastError string           // protected by mu
	RefCount  int              // protected by mu

	// --- Concurrency primitives ---
	mu sync.Mutex // protects all mutable fields above
}

// InputRelayManager manages all input relays (input URL -> local RTSP server)
//
// Concurrency notes:
// - All accesses to Relays map must hold mu.
// - Logger, recDir, rtspServer are set at construction and never changed.
type InputRelayManager struct {
	// Configuration registry for persistent input mappings
	inputConfigs map[string]*InputConfig // inputName -> InputConfig
	configMu     sync.RWMutex            // Protects inputConfigs

	// Configurable timeouts
	inputTimeout time.Duration

	// --- Mutable fields protected by mu ---
	Relays map[string]*InputRelay // inputURL -> InputRelay
	mu     sync.RWMutex           // Protects Relays map

	// --- Immutable/config fields (set at construction) ---
	Logger     *logger.Logger
	recDir     string
	rtspServer *RTSPServerManager
}

func NewInputRelayManager(l *logger.Logger, recDir string) *InputRelayManager {
	return &InputRelayManager{
		Relays:       make(map[string]*InputRelay),
		inputConfigs: make(map[string]*InputConfig),
		Logger:       l,
		recDir:       recDir,
		inputTimeout: 30 * time.Second, // Default
	}
}

// SetInputTimeout sets the timeout for waiting for input streams
func (irm *InputRelayManager) SetInputTimeout(timeout time.Duration) {
	irm.inputTimeout = timeout
}

// RegisterInputConfig stores an input configuration
func (irm *InputRelayManager) RegisterInputConfig(inputName, inputURL string) {
	irm.configMu.Lock()
	defer irm.configMu.Unlock()

	irm.inputConfigs[inputName] = &InputConfig{
		InputURL:  inputURL,
		InputName: inputName,
	}
	irm.Logger.Debug("Registered input config", "inputName", inputName, "inputURL", inputURL)
}

// GetInputURLByName returns the input URL for a given input name
func (irm *InputRelayManager) GetInputURLByName(inputName string) (string, bool) {
	// Check active relays first
	irm.mu.Lock()
	for inputURL, relay := range irm.Relays {
		if relay.InputName == inputName {
			irm.mu.Unlock()
			return inputURL, true
		}
	}
	irm.mu.Unlock()

	// Check stored configuration
	irm.configMu.RLock()
	defer irm.configMu.RUnlock()

	if config, exists := irm.inputConfigs[inputName]; exists {
		return config.InputURL, true
	}

	return "", false
}

// IncrementInputRef increments the reference count for an input
func (m *InputRelayManager) IncrementInputRef(inputURL, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	relay, exists := m.Relays[inputURL]
	if !exists {
		return
	}

	relay.mu.Lock()
	defer relay.mu.Unlock()
	relay.RefCount++
}

// DecrementInputRef decrements the reference count for an input relay
func (irm *InputRelayManager) DecrementInputRef(inputURL string, reason string) {
	irm.mu.Lock()
	relay, exists := irm.Relays[inputURL]
	irm.mu.Unlock()

	if !exists {
		return
	}

	relay.mu.Lock()
	relay.RefCount--
	refCount := relay.RefCount
	relay.mu.Unlock()

	irm.Logger.Info("DecrementInputRef", "inputURL", inputURL, "reason", reason, "newRefCount", refCount)

	if refCount <= 0 {
		irm.Logger.Info("Input ref count reached 0, stopping input", "inputURL", inputURL)
		// Stop the input but keep it in the map (preserves state/history)
		// Deletion is only performed by explicit user action (DeleteInput API)
		irm.stopInputRelayAtZero(inputURL)
	}
}

// stopInputRelayAtZero stops an input relay that has already reached refcount 0
// This is called internally by DecrementInputRef and does NOT decrement the refcount
func (irm *InputRelayManager) stopInputRelayAtZero(inputURL string) {
	irm.mu.Lock()
	relay, exists := irm.Relays[inputURL]
	if !exists {
		irm.mu.Unlock()
		return
	}
	relay.mu.Lock()

	// Only stop if refcount is actually 0 (guard against race conditions)
	if relay.RefCount != 0 {
		relay.mu.Unlock()
		irm.mu.Unlock()
		irm.Logger.Warn("stopInputRelayAtZero: refcount is not 0, skipping", "inputURL", inputURL, "refCount", relay.RefCount)
		return
	}

	proc := relay.Proc
	relay.Proc = nil
	relay.Status = InputStopped
	inputName := relay.InputName
	relay.mu.Unlock()
	irm.mu.Unlock()

	// Stop the process outside of any locks
	if proc != nil {
		err := proc.Stop(context.Background(), 2*time.Second)
		if err != nil {
			irm.Logger.Warn("Error stopping ffmpeg process", "inputURL", inputURL, "err", err)
		}
	}

	// Clean up RTSP stream
	if irm.rtspServer != nil && inputName != "" {
		relayPath := "relay/" + inputName
		irm.Logger.Debug("Cleaning up RTSP stream for stopped input relay", "relayPath", relayPath)
		irm.rtspServer.RemoveStream(relayPath)
	}
	Metrics.RelayStopTotal.WithLabelValues("input", "refcount_zero").Inc()
}

// SetRTSPServer sets the RTSP server instance
func (irm *InputRelayManager) SetRTSPServer(server *RTSPServerManager) {
	irm.rtspServer = server
}

// GetStream implements StreamProvider.GetStream
func (irm *InputRelayManager) GetStream(inputName string) (string, error) {
	inputURL, exists := irm.GetInputURLByName(inputName)
	if !exists {
		return "", fmt.Errorf("input configuration not found for: %s", inputName)
	}

	if irm.rtspServer == nil {
		return "", fmt.Errorf("RTSP server manager is not initialized")
	}

	// Compose local RTSP relay path and URL using the correct dynamic port
	relayPath := fmt.Sprintf("relay/%s", inputName)
	localRelayURL := irm.rtspServer.GetRTSPURL(relayPath)

	// Start the input relay with consumer counting
	localURL, err := irm.StartInputRelay(inputName, inputURL, localRelayURL, irm.inputTimeout)
	if err != nil {
		return "", fmt.Errorf("failed to start input relay for %s: %v", inputName, err)
	}

	// Wait for the RTSP stream to become ready (robust, with cleanup)
	irm.Logger.Info("Waiting for RTSP stream to become ready", "relayPath", relayPath)
	err = irm.rtspServer.WaitForStreamReady(relayPath, irm.inputTimeout)
	if err != nil {
		irm.Logger.Error("Failed to wait for RTSP stream to become ready", "inputName", inputName, "err", err)
		if !irm.rtspServer.IsStreamReady(relayPath) {
			irm.StopInputRelay(inputURL)
			return "", fmt.Errorf("RTSP stream not ready: %v", err)
		}
		irm.Logger.Warn("Stream appears ready but wait failed, continuing anyway", "relayPath", relayPath)
	}

	return localURL, nil
}

// ReleaseStream implements StreamProvider.ReleaseStream
func (irm *InputRelayManager) ReleaseStream(inputName string) {
	irm.Logger.Info("ReleaseStream called", "inputName", inputName)

	inputURL, exists := irm.GetInputURLByName(inputName)
	if !exists {
		irm.Logger.Warn("ReleaseStream: input name not found", "inputName", inputName)
		return
	}

	stopped := irm.StopInputRelay(inputURL)
	if stopped {
		irm.Logger.Info("Input relay stopped by consumer", "inputName", inputName)
	} else {
		irm.Logger.Debug("Input relay refcount decremented but not stopped", "inputName", inputName)
	}
}

// resolveInputURL checks if the inputURL is a file:// URL and returns the correct path for ffmpeg
func (irm *InputRelayManager) resolveInputURL(inputURL string) (string, error) {
	if strings.HasPrefix(inputURL, "file://") {
		relative := strings.TrimPrefix(inputURL, "file://")
		filePath := filepath.Join(irm.recDir, relative)
		if _, err := os.Stat(filePath); err != nil {
			return "", err
		}
		irm.Logger.Debug("Resolved input URL", "inputURL", inputURL, "filePath", filePath)
		return filePath, nil
	}
	return inputURL, nil
}

// StartInputRelay starts the input relay process if not running, returns local RTSP URL
// Increments reference count for each consumer
func (irm *InputRelayManager) StartInputRelay(inputName, inputURL, localURL string, timeout time.Duration) (string, error) {
	irm.Logger.Info("Starting input relay", "inputName", inputName, "inputURL", inputURL)
	// Resolve input URL (handle file://)
	resolvedInputURL, err := irm.resolveInputURL(inputURL)
	if err != nil {
		irm.Logger.Error("Failed to resolve input URL", "err", err)
		return "", err
	}
	irm.mu.Lock()
	relay, exists := irm.Relays[inputURL]
	if !exists {
		relay = &InputRelay{
			InputURL:  inputURL,
			InputName: inputName,
			LocalURL:  localURL,
			Status:    InputStopped,
			Timeout:   timeout,
			RefCount:  0,
		}
		irm.Relays[inputURL] = relay
		Metrics.ActiveInputRelays.Inc()
	}
	relay.mu.Lock()
	// If a relay already exists for this inputURL but with a different input name,
	// treat this as a conflict and return an error. This prevents multiple logical
	// names from being associated with the same input URL which could cause
	// confusion when cleaning up RTSP paths.
	if exists && relay.InputName != "" && relay.InputName != inputName {
		existingName := relay.InputName
		relay.mu.Unlock()
		irm.mu.Unlock()
		irm.Logger.Warn("Input URL already started with a different name", "inputURL", inputURL, "existingName", existingName, "requestedName", inputName)
		return "", fmt.Errorf("input URL %s already in use with name %s", inputURL, existingName)
	}
	// Increment reference count
	relay.RefCount++
	currentRefCount := relay.RefCount // Capture while holding lock
	irm.Logger.Debug("Incremented refcount", "inputURL", inputURL, "refcount", currentRefCount)
	if relay.Status == InputStarting || relay.Status == InputRunning {
		local := relay.LocalURL
		relay.mu.Unlock()
		irm.mu.Unlock()
		irm.Logger.Debug("Reusing existing relay", "inputURL", inputURL, "refcount", currentRefCount)
		Metrics.RelayStartTotal.WithLabelValues("input_reuse").Inc()
		return local, nil
	}
	relay.Status = InputStarting
	relay.LocalURL = localURL
	ctx := context.Background() // Use background context for now; can be enhanced for cancellation
	proc, err := NewFFmpegProcess(ctx, "-re", "-i", resolvedInputURL, "-c", "copy", "-f", "rtsp", "-rtsp_transport", "tcp", "-progress", "pipe:1", localURL)
	if err != nil {
		relay.Status = InputError
		relay.LastError = err.Error()
		relay.RefCount-- // Decrement on failure
		relay.mu.Unlock()
		irm.mu.Unlock()
		irm.Logger.Error("Failed to create input relay ffmpeg process", "err", err)
		return "", err
	}
	relay.Proc = proc
	err = proc.Start(ctx)
	if err != nil {
		relay.Status = InputError
		relay.LastError = err.Error()
		relay.RefCount-- // Decrement on failure
		relay.mu.Unlock()
		irm.mu.Unlock()
		irm.Logger.Error("Failed to start input relay ffmpeg", "err", err)
		return "", err
	}
	relay.Status = InputRunning
	relay.LastError = "" // Clear any previous error on successful start
	irm.Logger.Info("Started ffmpeg process", "PID", proc.GetPID(), "inputURL", inputURL, "localURL", localURL, "refcount", currentRefCount)
	Metrics.RelayStartTotal.WithLabelValues("input_new").Inc()
	Metrics.FFmpegProcessesActive.Inc()
	// Start process wait/monitor goroutine
	go irm.RunInputRelay(relay)
	local := relay.LocalURL
	relay.mu.Unlock()
	irm.mu.Unlock()
	return local, nil
}

// StopInputRelay decrements reference count and stops the input relay process only when refcount reaches 0
// This implements a reference counting mechanism to handle multiple consumers (recordings + output relays)
// Returns true if the relay was actually stopped (refcount reached 0)
func (irm *InputRelayManager) StopInputRelay(inputURL string) bool {
	irm.mu.Lock()
	relay, exists := irm.Relays[inputURL]
	if !exists {
		irm.Logger.Warn("relay for not found", "inputURL", inputURL)
		irm.mu.Unlock()
		return false
	}
	relay.mu.Lock()
	shouldStop := false
	var proc FFmpegProcess
	if relay.RefCount > 0 {
		relay.RefCount--
		currentRefCount := relay.RefCount
		irm.Logger.Debug("Decremented refcount", "inputURL", inputURL, "refcount", currentRefCount)
	} else {
		irm.Logger.Warn("refcount is already 0, cannot decrement", "inputURL", inputURL)
		relay.mu.Unlock()
		irm.mu.Unlock()
		return false
	}
	if relay.RefCount == 0 {
		shouldStop = true
		proc = relay.Proc
		relay.Proc = nil
		relay.Status = InputStopped
		Metrics.ActiveInputRelays.Dec()
	}
	inputName := relay.InputName
	relay.mu.Unlock()
	irm.mu.Unlock()

	if shouldStop && proc != nil {
		err := proc.Stop(context.Background(), 2*time.Second)
		if err != nil {
			irm.Logger.Warn("Error stopping ffmpeg process", "inputURL", inputURL, "err", err)
		}
	}
	// Clean up RTSP stream when input relay is fully stopped
	if shouldStop && irm.rtspServer != nil && inputName != "" {
		relayPath := "relay/" + inputName
		irm.Logger.Debug("Cleaning up RTSP stream for stopped input relay", "relayPath", relayPath)
		irm.rtspServer.RemoveStream(relayPath)
	}
	// Do NOT delete relay from map here. Deletion is only performed by explicit user action (DeleteInput).
	// This ensures relay state/history is preserved and avoids accidental resource loss.
	if shouldStop {
		Metrics.RelayStopTotal.WithLabelValues("input", "refcount_zero").Inc()
	}
	return shouldStop
}

// ForceStopInputRelay forcefully stops an input relay without regard to reference count
// This should only be used during shutdown or when there are refcount inconsistencies
func (irm *InputRelayManager) ForceStopInputRelay(inputURL string) bool {
	irm.Logger.Warn("Force stopping input relay", "inputURL", inputURL, "ignoring refcount")
	irm.mu.Lock()
	relay, exists := irm.Relays[inputURL]
	if !exists {
		irm.Logger.Warn("relay for not found", "inputURL", inputURL)
		irm.mu.Unlock()
		return false
	}
	relay.mu.Lock()
	currentRefCount := relay.RefCount
	irm.Logger.Warn("Force stopping relay", "inputURL", inputURL, "previous refcount", currentRefCount)
	proc := relay.Proc
	relay.RefCount = 0
	relay.Proc = nil
	relay.Status = InputStopped
	Metrics.ActiveInputRelays.Dec()
	inputName := relay.InputName
	relay.mu.Unlock()
	irm.mu.Unlock()

	if proc != nil {
		err := proc.Stop(context.Background(), 1*time.Second)
		if err != nil {
			irm.Logger.Warn("Error force stopping ffmpeg process", "inputURL", inputURL, "err", err)
		}
	}
	// Clean up RTSP stream when input relay is fully stopped
	if irm.rtspServer != nil && inputName != "" {
		relayPath := "relay/" + inputName
		irm.Logger.Debug("Cleaning up RTSP stream for force-stopped input relay", "relayPath", relayPath)
		irm.rtspServer.RemoveStream(relayPath)
	}
	Metrics.RelayStopTotal.WithLabelValues("input", "force").Inc()
	return true
}

// RunInputRelay runs and monitors the input relay process
func (irm *InputRelayManager) RunInputRelay(relay *InputRelay) {
	irm.Logger.Info("Running input relay", "inputURL", relay.InputURL, "localURL", relay.LocalURL)
	startTime := time.Now()
	var proc FFmpegProcess
	relay.mu.Lock()
	proc = relay.Proc
	relay.mu.Unlock()
	if proc == nil {
		irm.Logger.Error("RunInputRelay: FFmpegProcess is nil", "inputURL", relay.InputURL)
		return
	}
	err := proc.Wait()
	output := proc.GetOutput()

	relay.mu.Lock()
	status := relay.Status
	inputURL := relay.InputURL
	intentional := relay.RefCount == 0 // If refcount is 0, this was an intentional stop
	if err != nil {
		if intentional {
			relay.Status = InputStopped
			relay.LastError = ""
		} else {
			relay.Status = InputError
			relay.LastError = err.Error()
		}
	}
	if err == nil {
		relay.Status = InputStopped
	}
	relay.Proc = nil
	relay.mu.Unlock()

	Metrics.FFmpegProcessesActive.Dec()
	Metrics.FFmpegProcessDuration.WithLabelValues("input").Observe(time.Since(startTime).Seconds())

	if status == InputStopped {
		if err != nil {
			irm.Logger.Info("Input relay stopped", "inputURL", inputURL, "signal", err)
		} else {
			irm.Logger.Info("Input relay stopped cleanly", "inputURL", inputURL)
		}
		return
	}
	if err != nil {
		irm.Logger.Error("Input relay process exited with error", "inputURL", inputURL, "PID", proc.GetPID(), "err", err)
		irm.Logger.Error("[ffmpeg output] for %s:\n%s", inputURL, output)
		Metrics.RelayErrorsTotal.WithLabelValues("input", "process_exit").Inc()
		Metrics.FFmpegProcessErrorTotal.WithLabelValues("input", "error").Inc()
	} else {
		irm.Logger.Info("Input relay process completed successfully", "inputURL", inputURL, "PID", proc.GetPID())
	}
}

// GetInputNameForURL returns the input name for a given input URL
func (irm *InputRelayManager) GetInputNameForURL(inputURL string) string {
	irm.mu.Lock()
	defer irm.mu.Unlock()

	if relay, exists := irm.Relays[inputURL]; exists {
		return relay.InputName
	}
	return ""
}

// FindLocalURLByInputName returns the local RTSP URL for a given inputName, concurrency-safe.
func (irm *InputRelayManager) FindLocalURLByInputName(inputName string) (string, bool) {
	irm.mu.Lock()
	defer irm.mu.Unlock()
	for _, relay := range irm.Relays {
		if relay.InputName == inputName {
			return relay.LocalURL, true
		}
	}
	return "", false
}

// DeleteInput completely removes an input relay and all associated outputs
func (irm *InputRelayManager) DeleteInput(inputURL string) error {

	irm.Logger.Info("Deleting input", "inputURL", inputURL)
	irm.mu.Lock()
	relay, exists := irm.Relays[inputURL]
	if !exists {
		irm.Logger.Warn("relay for not found", "inputURL", inputURL)
		irm.mu.Unlock()
		return fmt.Errorf("input relay not found: %s", inputURL)
	}
	relay.mu.Lock()
	// Double-check refcount to prevent race condition where input was resurrected
	if relay.RefCount > 0 {
		relay.mu.Unlock()
		irm.mu.Unlock()
		irm.Logger.Info("DeleteInput aborted: input resurrected", "inputURL", inputURL, "refCount", relay.RefCount)
		return nil
	}
	irm.Logger.Info("DeleteInput proceeding", "inputURL", inputURL, "refCount", relay.RefCount)

	proc := relay.Proc
	relay.Proc = nil
	relay.Status = InputStopped
	inputName := relay.InputName
	relay.mu.Unlock()
	// Remove from map before stopping process
	delete(irm.Relays, inputURL)
	irm.mu.Unlock()

	// Stop the process outside of any locks
	if proc != nil {
		err := proc.Stop(context.Background(), 1*time.Second)
		if err != nil {
			irm.Logger.Warn("Error deleting ffmpeg process", "inputURL", inputURL, "err", err)
		}
	}

	// Clean up RTSP stream
	if irm.rtspServer != nil && inputName != "" {
		relayPath := "relay/" + inputName
		irm.Logger.Debug("Cleaning up RTSP stream for deleted input relay", "relayPath", relayPath)
		irm.rtspServer.RemoveStream(relayPath)
	}
	irm.Logger.Info("Input relay deleted successfully", "inputURL", inputURL)
	Metrics.RelayStopTotal.WithLabelValues("input", "delete").Inc()
	return nil
}

// GetRelayStatus returns the current status and refcount of an input relay safely
func (irm *InputRelayManager) GetRelayStatus(inputURL string) (InputRelayStatus, int, bool) {
	irm.mu.Lock()
	relay, exists := irm.Relays[inputURL]
	irm.mu.Unlock()

	if !exists {
		return 0, 0, false
	}

	relay.mu.Lock()
	defer relay.mu.Unlock()
	return relay.Status, relay.RefCount, true
}

// GetRunningInputRelays returns a list of currently running input relays
// This returns pointers to the relays. Callers must be careful with concurrency
// or use the snapshot methods.
func (irm *InputRelayManager) GetRunningInputRelays() []*InputRelay {
	irm.mu.RLock()
	defer irm.mu.RUnlock()

	relays := make([]*InputRelay, 0, len(irm.Relays))
	for _, relay := range irm.Relays {
		relays = append(relays, relay)
	}
	return relays
}
