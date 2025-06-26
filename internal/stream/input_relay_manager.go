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
		irm.Logger.Debug("Resolved input URL: %s -> %s", inputURL, filePath)
		return filePath, nil
	}
	return inputURL, nil
}

// StartInputRelay starts the input relay process if not running, returns local RTSP URL
// Increments reference count for each consumer
func (irm *InputRelayManager) StartInputRelay(inputName, inputURL, localURL string, timeout time.Duration) (string, error) {
	irm.Logger.Info("InputRelayManager: StartInputRelay: inputName=%s, inputURL=%s", inputName, inputURL)
	// Resolve input URL (handle file://)
	resolvedInputURL, err := irm.resolveInputURL(inputURL)
	if err != nil {
		irm.Logger.Error("Failed to resolve input URL: %v", err)
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
	irm.Logger.Debug("InputRelayManager: Incremented refcount for %s to %d", inputURL, currentRefCount)
	if relay.Status == InputStarting || relay.Status == InputRunning {
		local := relay.LocalURL
		relay.mu.Unlock()
		irm.mu.Unlock()
		irm.Logger.Debug("InputRelayManager: Reusing existing relay for %s (refcount: %d)", inputURL, currentRefCount)
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
		irm.Logger.Error("Failed to create input relay ffmpeg process: %v", err)
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
		irm.Logger.Error("Failed to start input relay ffmpeg: %v", err)
		return "", err
	}
	relay.Status = InputRunning
	irm.Logger.Info("InputRelayManager: Started ffmpeg process PID %d for %s -> %s (refcount: %d)", proc.PID, inputURL, localURL, currentRefCount)
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
	irm.Logger.Info("InputRelayManager: StopInputRelay: inputURL=%s", inputURL)
	irm.mu.Lock()
	relay, exists := irm.Relays[inputURL]
	if !exists {
		irm.Logger.Warn("InputRelayManager: relay for %s not found", inputURL)
		irm.mu.Unlock()
		return false
	}
	relay.mu.Lock()
	shouldStop := false
	var proc *FFmpegProcess
	if relay.RefCount > 0 {
		relay.RefCount--
		currentRefCount := relay.RefCount
		irm.Logger.Debug("InputRelayManager: Decremented refcount for %s to %d", inputURL, currentRefCount)
	} else {
		irm.Logger.Warn("InputRelayManager: refcount for %s is already 0, cannot decrement", inputURL)
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
			irm.Logger.Warn("InputRelayManager: Error stopping ffmpeg process for %s: %v", inputURL, err)
		}
	}
	// Clean up RTSP stream when input relay is fully stopped
	if shouldStop && irm.rtspServer != nil && inputName != "" {
		relayPath := "relay/" + inputName
		irm.Logger.Debug("InputRelayManager: Cleaning up RTSP stream for stopped input relay: %s", relayPath)
		irm.rtspServer.RemoveStream(relayPath)
	}
	// Do NOT delete relay from map here. Deletion is only performed by explicit user action (DeleteInput).
	// This ensures relay state/history is preserved and avoids accidental resource loss.
	return shouldStop
}

// ForceStopInputRelay forcefully stops an input relay without regard to reference count
// This should only be used during shutdown or when there are refcount inconsistencies
func (irm *InputRelayManager) ForceStopInputRelay(inputURL string) bool {
	irm.Logger.Warn("InputRelayManager: ForceStopInputRelay: inputURL=%s (ignoring refcount)", inputURL)
	irm.mu.Lock()
	relay, exists := irm.Relays[inputURL]
	if !exists {
		irm.Logger.Warn("InputRelayManager: relay for %s not found", inputURL)
		irm.mu.Unlock()
		return false
	}
	relay.mu.Lock()
	currentRefCount := relay.RefCount
	irm.Logger.Warn("InputRelayManager: Force stopping relay %s (previous refcount: %d)", inputURL, currentRefCount)
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
			irm.Logger.Warn("InputRelayManager: Error force stopping ffmpeg process for %s: %v", inputURL, err)
		}
	}
	// Clean up RTSP stream when input relay is fully stopped
	if irm.rtspServer != nil && inputName != "" {
		relayPath := "relay/" + inputName
		irm.Logger.Debug("InputRelayManager: Cleaning up RTSP stream for force-stopped input relay: %s", relayPath)
		irm.rtspServer.RemoveStream(relayPath)
	}
	return true
}

// RunInputRelay runs and monitors the input relay process
func (irm *InputRelayManager) RunInputRelay(relay *InputRelay) {
	irm.Logger.Info("InputRelayManager: RunInputRelay: running ffmpeg for %s -> %s", relay.InputURL, relay.LocalURL)
	var proc *FFmpegProcess
	relay.mu.Lock()
	proc = relay.Proc
	relay.mu.Unlock()
	if proc == nil {
		irm.Logger.Error("InputRelayManager: RunInputRelay: FFmpegProcess is nil for %s", relay.InputURL)
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
			irm.Logger.Info("Input relay for %s stopped (signal: %v)", inputURL, err)
		} else {
			irm.Logger.Info("Input relay for %s stopped cleanly", inputURL)
		}
		return
	}
	if err != nil {
		irm.Logger.Error("Input relay process exited with error for %s (PID=%d): %v", inputURL, proc.PID, err)
		irm.Logger.Error("[ffmpeg output] for %s:\n%s", inputURL, output)
	} else {
		irm.Logger.Info("Input relay process for %s completed successfully (PID=%d)", inputURL, proc.PID)
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
	irm.Logger.Info("InputRelayManager: DeleteInput: inputURL=%s", inputURL)
	irm.mu.Lock()
	relay, exists := irm.Relays[inputURL]
	if !exists {
		irm.Logger.Warn("InputRelayManager: relay for %s not found", inputURL)
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
			irm.Logger.Warn("InputRelayManager: Error deleting ffmpeg process for %s: %v", inputURL, err)
		}
	}

	// Clean up RTSP stream
	if irm.rtspServer != nil && inputName != "" {
		relayPath := "relay/" + inputName
		irm.Logger.Debug("InputRelayManager: Cleaning up RTSP stream for deleted input relay: %s", relayPath)
		irm.rtspServer.RemoveStream(relayPath)
	}
	irm.Logger.Info("InputRelayManager: Input relay %s deleted successfully", inputURL)
	return nil
}
