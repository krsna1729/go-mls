package stream

import (
	"bufio"
	"context"
	"fmt"
	"go-mls/internal/logger"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
	PID       int // Cached process ID to avoid race conditions
	Status    InputRelayStatus
	LastError string
	StartTime time.Time
	Timeout   time.Duration
	RefCount  int       // Number of consumers (output relays + recordings)
	Speed     float64   // Current speed (e.g., 1.01x)
	LastSpeed time.Time // Last time speed was updated
	mu        sync.Mutex
	ctx       context.Context    // Context for cancellation
	cancel    context.CancelFunc // Cancel function for graceful shutdown
	wg        sync.WaitGroup     // Wait group for goroutines
}

// InputRelayManager manages all input relays
// (input URL -> local RTSP server)
type InputRelayManager struct {
	Relays     map[string]*InputRelay // key: input URL
	mu         sync.Mutex
	Logger     *logger.Logger
	recDir     string             // Directory for playing recordings from
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
		ctx, cancel := context.WithCancel(context.Background())
		relay = &InputRelay{
			InputURL:  inputURL,
			InputName: inputName,
			LocalURL:  localURL,
			Status:    InputStopped,
			Timeout:   timeout,
			RefCount:  0,
			ctx:       ctx,
			cancel:    cancel,
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
	cmd := exec.Command("ffmpeg",
		"-re", "-i", resolvedInputURL, "-c", "copy", "-f", "rtsp", "-rtsp_transport", "tcp",
		"-progress", "pipe:1", // Use progress output for easier parsing
		localURL,
	)
	// Set up process group to prevent child processes from receiving SIGINT on Ctrl+C
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	relay.Cmd = cmd
	relay.Status = InputRunning
	relay.StartTime = time.Now()

	// Set up stdout pipe for progress monitoring (prefer progress over stderr)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		relay.Status = InputError
		relay.LastError = err.Error()
		relay.RefCount-- // Decrement on failure
		relay.mu.Unlock()
		irm.mu.Unlock()
		irm.Logger.Error("Failed to create stdout pipe for input relay: %v", err)
		return "", err
	}

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

	// Cache the PID to avoid race conditions when accessing it later
	relay.PID = cmd.Process.Pid
	irm.Logger.Info("InputRelayManager: Started ffmpeg process PID %d for %s -> %s (refcount: %d)", relay.PID, inputURL, localURL, currentRefCount)

	// Start bitrate monitoring with context
	relay.wg.Add(1)
	go func() {
		defer relay.wg.Done()
		irm.monitorFFmpegProgress(relay, stdoutPipe)
	}()

	relay.wg.Add(1)
	go func() {
		defer relay.wg.Done()
		irm.RunInputRelay(relay)
	}()
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

	// Decrement reference count safely
	// Each consumer (recording/output relay) should call this once when stopping
	if relay.RefCount > 0 {
		relay.RefCount--
		currentRefCount := relay.RefCount // Capture while holding lock
		irm.Logger.Debug("InputRelayManager: Decremented refcount for %s to %d", inputURL, currentRefCount)
	} else {
		// This should not happen in normal operation - indicates a bug
		irm.Logger.Warn("InputRelayManager: refcount for %s is already 0, cannot decrement", inputURL)
		relay.mu.Unlock()
		irm.mu.Unlock()
		return false
	}

	// Only stop the relay if no more consumers are using it
	// This is the key to the reference counting system
	if relay.RefCount > 0 {
		currentRefCount := relay.RefCount // Capture while holding lock
		irm.Logger.Info("InputRelayManager: Not stopping relay for %s, still has %d consumers", inputURL, currentRefCount)
		relay.mu.Unlock()
		irm.mu.Unlock()
		return false
	}

	// RefCount is 0 - safe to stop the relay process
	// Terminate the process gracefully with fallback to force kill
	irm.terminateProcess(relay.Cmd, relay.PID, inputURL, 2)

	// Cancel context to signal all monitoring goroutines to stop
	// This ensures clean shutdown of progress monitoring and other background tasks
	if relay.cancel != nil {
		relay.cancel()
	}

	relay.Status = InputStopped
	relay.Cmd = nil

	// Wait for all goroutines to finish before continuing cleanup
	// This prevents race conditions during relay cleanup
	relay.mu.Unlock()
	irm.Logger.Debug("InputRelayManager: Waiting for goroutines to finish for %s", inputURL)

	// Wait for goroutines with timeout to prevent hanging during shutdown
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.wg.Wait()
	}()

	select {
	case <-done:
		irm.Logger.Debug("InputRelayManager: All goroutines finished for %s", inputURL)
	case <-time.After(5 * time.Second):
		irm.Logger.Warn("InputRelayManager: Timeout waiting for goroutines to finish for %s, force killing process", inputURL)
		// Force kill the process if goroutines are still stuck
		if relay.Cmd != nil && relay.Cmd.Process != nil {
			pid := relay.PID
			irm.Logger.Warn("InputRelayManager: Force killing stuck process PID %d for %s", pid, inputURL)
			relay.Cmd.Process.Kill()
		}
		// Wait a bit more for force kill to take effect
		select {
		case <-done:
			irm.Logger.Debug("InputRelayManager: Goroutines finished after force kill for %s", inputURL)
		case <-time.After(2 * time.Second):
			irm.Logger.Error("InputRelayManager: Goroutines still stuck after force kill for %s, proceeding anyway", inputURL)
		}
	}

	relay.mu.Lock()

	inputName := relay.InputName // Store input name for RTSP cleanup
	relay.mu.Unlock()

	// Clean up RTSP stream when input relay is fully stopped
	// This removes the stream from the RTSP server's routing table
	if irm.rtspServer != nil && inputName != "" {
		relayPath := "relay/" + inputName
		irm.Logger.Debug("InputRelayManager: Cleaning up RTSP stream for stopped input relay: %s", relayPath)
		irm.rtspServer.RemoveStream(relayPath)
	}

	irm.mu.Unlock()

	return true
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

	currentRefCount := relay.RefCount // Capture current refcount for logging
	irm.Logger.Warn("InputRelayManager: Force stopping relay %s (previous refcount: %d)", inputURL, currentRefCount)

	// Force refcount to 0 to ensure cleanup
	relay.RefCount = 0

	// Stop the relay process
	irm.terminateProcess(relay.Cmd, relay.PID, inputURL, 1) // Shorter timeout for force stop

	// Cancel context to stop monitoring goroutines
	if relay.cancel != nil {
		relay.cancel()
	}

	relay.Status = InputStopped
	relay.Cmd = nil

	// Wait for all goroutines to finish before continuing
	relay.mu.Unlock()
	irm.Logger.Debug("InputRelayManager: Waiting for goroutines to finish for %s", inputURL)

	// Wait for goroutines with timeout to prevent hanging during shutdown
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.wg.Wait()
	}()

	select {
	case <-done:
		irm.Logger.Debug("InputRelayManager: All goroutines finished for %s", inputURL)
	case <-time.After(5 * time.Second):
		irm.Logger.Warn("InputRelayManager: Timeout waiting for goroutines to finish for %s, force killing process", inputURL)
		// Force kill the process if goroutines are still stuck
		if relay.Cmd != nil && relay.Cmd.Process != nil {
			pid := relay.PID
			irm.Logger.Warn("InputRelayManager: Force killing stuck process PID %d for %s", pid, inputURL)
			relay.Cmd.Process.Kill()
		}
		// Wait a bit more for force kill to take effect
		select {
		case <-done:
			irm.Logger.Debug("InputRelayManager: Goroutines finished after force kill for %s", inputURL)
		case <-time.After(2 * time.Second):
			irm.Logger.Error("InputRelayManager: Goroutines still stuck after force kill for %s, proceeding anyway", inputURL)
		}
	}

	relay.mu.Lock()

	inputName := relay.InputName // Store input name for RTSP cleanup
	relay.mu.Unlock()

	// Clean up RTSP stream when input relay is fully stopped
	if irm.rtspServer != nil && inputName != "" {
		relayPath := "relay/" + inputName
		irm.Logger.Debug("InputRelayManager: Cleaning up RTSP stream for force-stopped input relay: %s", relayPath)
		irm.rtspServer.RemoveStream(relayPath)
	}

	irm.mu.Unlock()

	return true
}

// RunInputRelay runs and monitors the input relay process
func (irm *InputRelayManager) RunInputRelay(relay *InputRelay) {
	irm.Logger.Info("InputRelayManager: RunInputRelay: running ffmpeg for %s -> %s", relay.InputURL, relay.LocalURL)

	// Capture PID at the beginning for consistent logging
	var pid string = "unknown"
	if relay.Cmd != nil && relay.Cmd.Process != nil {
		pid = fmt.Sprintf("%d", relay.PID)
	}

	err := relay.Cmd.Wait()
	relay.mu.Lock()

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

	// Force stop the relay process regardless of reference count
	irm.terminateProcess(relay.Cmd, relay.PID, inputURL, 1) // Shorter timeout for delete

	// Cancel context to stop monitoring goroutines
	if relay.cancel != nil {
		relay.cancel()
	}

	relay.Status = InputStopped
	relay.Cmd = nil
	inputName := relay.InputName // Store input name for RTSP cleanup

	// Wait for all goroutines to finish
	relay.mu.Unlock()
	irm.Logger.Debug("InputRelayManager: Waiting for goroutines to finish for %s", inputURL)

	// Wait for goroutines with timeout to prevent hanging during delete
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.wg.Wait()
	}()

	select {
	case <-done:
		irm.Logger.Debug("InputRelayManager: All goroutines finished for %s", inputURL)
	case <-time.After(3 * time.Second):
		irm.Logger.Warn("InputRelayManager: Timeout waiting for goroutines to finish for %s during delete, proceeding anyway", inputURL)
	}

	relay.mu.Lock()

	// Remove from map
	delete(irm.Relays, inputURL)
	relay.mu.Unlock()

	// Clean up RTSP stream
	if irm.rtspServer != nil && inputName != "" {
		relayPath := "relay/" + inputName
		irm.Logger.Debug("InputRelayManager: Cleaning up RTSP stream for deleted input relay: %s", relayPath)
		irm.rtspServer.RemoveStream(relayPath)
	}

	irm.mu.Unlock()
	irm.Logger.Info("InputRelayManager: Input relay %s deleted successfully", inputURL)
	return nil
}

// monitorFFmpegProgress monitors ffmpeg -progress output for speed information
func (irm *InputRelayManager) monitorFFmpegProgress(relay *InputRelay, progressReader io.Reader) {
	defer func() {
		if r := recover(); r != nil {
			irm.Logger.Warn("InputRelay progress monitoring panic recovered for %s: %v", relay.InputURL, r)
		}
	}()

	scanner := bufio.NewScanner(progressReader)
	lineChan := make(chan string, 10) // Buffered channel to prevent blocking
	errChan := make(chan error, 1)

	// Run scanner in a separate goroutine to avoid blocking on Scan()
	go func() {
		defer close(lineChan)
		defer close(errChan)
		for scanner.Scan() {
			select {
			case lineChan <- scanner.Text():
			case <-relay.ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case errChan <- err:
			case <-relay.ctx.Done():
			}
		}
	}()

	// Process lines with context cancellation support
	for {
		select {
		case <-relay.ctx.Done():
			irm.Logger.Debug("InputRelay progress monitoring cancelled for %s", relay.InputURL)
			return
		case line, ok := <-lineChan:
			if !ok {
				// Channel closed, scanner finished
				select {
				case err := <-errChan:
					if err != nil {
						irm.Logger.Debug("InputRelay progress monitoring scanner error for %s: %v", relay.InputURL, err)
					}
				default:
				}
				irm.Logger.Debug("InputRelay progress monitoring finished for %s", relay.InputURL)
				return
			}

			// Process the line
			if strings.HasPrefix(line, "speed=") {
				val := strings.TrimPrefix(line, "speed=")
				// Remove 'x' suffix (e.g., "1.01x" -> "1.01")
				val = strings.TrimSuffix(val, "x")
				val = strings.TrimSpace(val)
				if val == "N/A" || val == "" {
					irm.Logger.Debug("InputRelay speed N/A for %s", relay.InputURL)
					continue // Do not update speed if N/A
				}
				if speed, err := strconv.ParseFloat(val, 64); err == nil {
					relay.mu.Lock()
					relay.Speed = speed
					relay.LastSpeed = time.Now()
					relay.mu.Unlock()
					irm.Logger.Debug("InputRelay speed updated: %s -> %.2fx", relay.InputURL, speed)
				} else {
					irm.Logger.Warn("Failed to parse InputRelay speed for %s: %s (error: %v)", relay.InputURL, val, err)
				}
			}
		}
	}
}

// terminateProcess safely terminates a process with graceful shutdown followed by force kill if necessary
// This avoids race conditions by not calling Wait() concurrently - the RunInputRelay goroutine handles Wait()
func (irm *InputRelayManager) terminateProcess(cmd *exec.Cmd, pid int, inputURL string, timeoutSeconds int) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	irm.Logger.Info("InputRelayManager: Gracefully terminating ffmpeg process PID %d for %s", pid, inputURL)

	// Try SIGTERM first for graceful shutdown
	err := cmd.Process.Signal(os.Interrupt)
	if err != nil {
		irm.Logger.Warn("Failed to send SIGTERM to input relay process PID %d: %v", pid, err)
	}

	// Give process time to gracefully exit
	time.Sleep(time.Duration(timeoutSeconds) * time.Second)

	// Check if process is still alive and force kill if necessary
	// We don't call Wait() here to avoid race condition with RunInputRelay goroutine
	if cmd.Process != nil {
		// Send kill signal - this will cause the Wait() in RunInputRelay to return
		err = cmd.Process.Kill()
		if err != nil {
			// Process might have already exited - this is fine
			irm.Logger.Debug("Kill signal for process PID %d failed (likely already exited): %v", pid, err)
		} else {
			irm.Logger.Debug("InputRelayManager: Sent SIGKILL to process PID %d for %s", pid, inputURL)
		}
	}
}
