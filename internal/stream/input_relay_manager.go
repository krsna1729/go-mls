package stream

import (
	"fmt"
	"go-mls/internal/logger"
	"os"
	"os/exec"
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

// InputRelay represents a single input ffmpeg process
// and its state
type InputRelay struct {
	InputURL  string
	InputName string
	LocalURL  string // Local RTSP server URL
	Cmd       *exec.Cmd
	Status    InputRelayStatus
	LastError string
	StartTime time.Time
	Timeout   time.Duration
	RefCount  int // Number of consumers (output relays + recordings)
	mu        sync.Mutex
}

// InputRelayManager manages all input relays
// (input URL -> local RTSP server)
type InputRelayManager struct {
	Relays     map[string]*InputRelay // key: input URL
	mu         sync.Mutex
	Logger     *logger.Logger
	recDir     string // Directory for playing recordings from
	rtspServer *RTSPServerManager // RTSP server for cleanup
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
	irm.Logger.Debug("InputRelayManager: Incremented refcount for %s to %d", inputURL, relay.RefCount)

	if relay.Status == InputStarting || relay.Status == InputRunning {
		local := relay.LocalURL
		relay.mu.Unlock()
		irm.mu.Unlock()
		irm.Logger.Debug("InputRelayManager: Reusing existing relay for %s (refcount: %d)", inputURL, relay.RefCount)
		return local, nil
	}
	relay.Status = InputStarting
	relay.LocalURL = localURL
	cmd := exec.Command("ffmpeg",
		"-re", "-i", resolvedInputURL, "-c", "copy", "-f", "rtsp", "-rtsp_transport", "tcp", localURL)
	relay.Cmd = cmd
	relay.Status = InputRunning
	relay.StartTime = time.Now()

	// Log the ffmpeg command and start it
	irm.Logger.Debug("InputRelayManager: Starting ffmpeg command: %v", cmd.Args)
	if err := cmd.Start(); err != nil {
		relay.Status = InputError
		relay.LastError = err.Error()
		relay.RefCount-- // Decrement on failure
		relay.mu.Unlock()
		irm.mu.Unlock()
		irm.Logger.Error("Failed to start input relay ffmpeg: %v", err)
		return "", err
	}
	irm.Logger.Info("InputRelayManager: Started ffmpeg process PID %d for %s -> %s (refcount: %d)", cmd.Process.Pid, inputURL, localURL, relay.RefCount)

	go irm.RunInputRelay(relay)
	local := relay.LocalURL
	relay.mu.Unlock()
	irm.mu.Unlock()
	return local, nil
}

// StopInputRelay decrements reference count and stops the input relay process only when refcount reaches 0
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

	// Decrement reference count
	if relay.RefCount > 0 {
		relay.RefCount--
		irm.Logger.Debug("InputRelayManager: Decremented refcount for %s to %d", inputURL, relay.RefCount)
	} else {
		irm.Logger.Warn("InputRelayManager: refcount for %s is already 0, cannot decrement", inputURL)
	}

	// Only stop if no more consumers
	if relay.RefCount > 0 {
		irm.Logger.Info("InputRelayManager: Not stopping relay for %s, still has %d consumers", inputURL, relay.RefCount)
		relay.mu.Unlock()
		irm.mu.Unlock()
		return false
	}

	// Stop the relay process
	if relay.Cmd != nil && relay.Cmd.Process != nil {
		pid := relay.Cmd.Process.Pid
		irm.Logger.Info("InputRelayManager: Gracefully terminating ffmpeg process PID %d for %s", pid, inputURL)
		// Try SIGTERM first for graceful shutdown
		err := relay.Cmd.Process.Signal(os.Interrupt)
		if err != nil {
			irm.Logger.Warn("Failed to send SIGTERM to input relay process PID %d: %v", pid, err)
			// Fallback to SIGKILL
			err = relay.Cmd.Process.Kill()
			if err != nil {
				irm.Logger.Error("Failed to kill input relay process PID %d: %v", pid, err)
			}
		}
	}
	relay.Status = InputStopped
	relay.Cmd = nil
	inputName := relay.InputName // Store input name for RTSP cleanup
	relay.mu.Unlock()
	
	// Clean up RTSP stream when input relay is fully stopped
	if irm.rtspServer != nil && inputName != "" {
		relayPath := "relay/" + inputName
		irm.Logger.Debug("InputRelayManager: Cleaning up RTSP stream for stopped input relay: %s", relayPath)
		irm.rtspServer.RemoveStream(relayPath)
	}
	
	irm.mu.Unlock()
	
	return true
}

// RunInputRelay runs and monitors the input relay process
func (irm *InputRelayManager) RunInputRelay(relay *InputRelay) {
	irm.Logger.Info("InputRelayManager: RunInputRelay: running ffmpeg for %s -> %s", relay.InputURL, relay.LocalURL)
	err := relay.Cmd.Wait()
	relay.mu.Lock()
	pid := "unknown"
	if relay.Cmd != nil && relay.Cmd.Process != nil {
		pid = fmt.Sprintf("%d", relay.Cmd.Process.Pid)
	}
	if relay.Status == InputStopped {
		if err != nil {
			irm.Logger.Info("Input relay PID %s for %s stopped (signal: %v)", pid, relay.InputURL, err)
		} else {
			irm.Logger.Info("Input relay PID %s for %s stopped cleanly", pid, relay.InputURL)
		}
		relay.mu.Unlock()
		return
	}
	if err != nil {
		relay.Status = InputError
		relay.LastError = err.Error()
		irm.Logger.Error("Input relay process PID %s exited with error for %s: %v", pid, relay.InputURL, err)
	} else {
		relay.Status = InputStopped
		irm.Logger.Info("Input relay process PID %s for %s completed successfully", pid, relay.InputURL)
	}
	relay.Cmd = nil
	relay.mu.Unlock()
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
