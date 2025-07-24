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
	Proc      *FFmpegProcess   // may be replaced on restart, protected by mu
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
	Relays     map[string]*InputRelay // key: input URL, protected by mu
	mu         sync.Mutex             // protects Relays
	Logger     *logger.Logger         // immutable
	recDir     string                 // immutable
	rtspServer *RTSPServerManager     // set at construction or via SetRTSPServer
}

func NewInputRelayManager(l *logger.Logger, recDir string) *InputRelayManager {
	return &InputRelayManager{
		Relays: make(map[string]*InputRelay),
		Logger: l,
		recDir: recDir,
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
	}
	relay.mu.Lock()
	// Increment reference count
	relay.RefCount++
	currentRefCount := relay.RefCount // Capture while holding lock
	irm.Logger.Debug("Incremented refcount", "inputURL", inputURL, "refcount", currentRefCount)
	if relay.Status == InputStarting || relay.Status == InputRunning {
		local := relay.LocalURL
		relay.mu.Unlock()
		irm.mu.Unlock()
		irm.Logger.Debug("Reusing existing relay", "inputURL", inputURL, "refcount", currentRefCount)
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
	err = proc.Start()
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
	irm.Logger.Info("Started ffmpeg process", "PID", proc.PID, "inputURL", inputURL, "localURL", localURL, "refcount", currentRefCount)
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
	var proc *FFmpegProcess
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
	}
	inputName := relay.InputName
	relay.mu.Unlock()
	irm.mu.Unlock()

	if shouldStop && proc != nil {
		err := proc.Stop(2 * time.Second)
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
	inputName := relay.InputName
	relay.mu.Unlock()
	irm.mu.Unlock()

	if proc != nil {
		err := proc.Stop(1 * time.Second)
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
	return true
}

// RunInputRelay runs and monitors the input relay process
func (irm *InputRelayManager) RunInputRelay(relay *InputRelay) {
	irm.Logger.Info("Running input relay", "inputURL", relay.InputURL, "localURL", relay.LocalURL)
	var proc *FFmpegProcess
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

	if status == InputStopped {
		if err != nil {
			irm.Logger.Info("Input relay stopped", "inputURL", inputURL, "signal", err)
		} else {
			irm.Logger.Info("Input relay stopped cleanly", "inputURL", inputURL)
		}
		return
	}
	if err != nil {
		irm.Logger.Error("Input relay process exited with error", "inputURL", inputURL, "PID", proc.PID, "err", err)
		irm.Logger.Error("[ffmpeg output] for %s:\n%s", inputURL, output)
	} else {
		irm.Logger.Info("Input relay process completed successfully", "inputURL", inputURL, "PID", proc.PID)
	}
}

// SetRTSPServer sets the RTSP server instance for stream cleanup
func (irm *InputRelayManager) SetRTSPServer(server *RTSPServerManager) {
	irm.rtspServer = server
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
		err := proc.Stop(1 * time.Second)
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
	return nil
}
