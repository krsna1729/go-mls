package stream

import (
	"context"
	"fmt"
	"go-mls/internal/logger"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Recording represents a recording session or file
type Recording struct {
	// --- Fields exposed to API/JSON ---
	Name      string    `json:"name"`
	Source    string    `json:"source"`
	Filename  string    `json:"filename"`
	FileSize  int64     `json:"file_size"`
	StartedAt time.Time `json:"started_at"`
	StoppedAt time.Time `json:"stopped_at,omitempty"`
	Active    bool      `json:"active"`

	// --- Internal fields (not exposed to API) ---
	FilePath string `json:"-"` // Full filesystem path - security sensitive
}

// RecordingManager manages active and completed recordings
// Now uses RelayManager for local relay and refcounting
type RecordingManager struct {
	// --- Mutable fields protected by mu ---
	mu         sync.RWMutex
	recordings map[string]*Recording
	processes  map[string]FFmpegProcess // Now uses FFmpegProcess abstraction
	dones      map[string]chan struct{} // done channel for each recording

	// --- Immutable/config fields (set at construction) ---
	Logger   *logger.Logger // Logger
	dir      string         // Recordings directory
	RelayMgr *RelayManager  // Reference to RelayManager for local relay

	// --- Shutdown support ---
	ctx       context.Context
	cancel    context.CancelFunc
	watcherWg sync.WaitGroup
}

// NewRecordingManager creates a RecordingManager and ensures the directory exists
func NewRecordingManager(l *logger.Logger, dir string, relayMgr *RelayManager) *RecordingManager {
	if err := os.MkdirAll(dir, 0755); err != nil {
		panic(fmt.Sprintf("Failed to create recordings directory: %v", err))
	}
	ctx, cancel := context.WithCancel(context.Background())
	rm := &RecordingManager{
		recordings: make(map[string]*Recording),
		processes:  make(map[string]FFmpegProcess),
		dones:      make(map[string]chan struct{}),
		Logger:     l,
		dir:        dir,
		RelayMgr:   relayMgr,
		ctx:        ctx,
		cancel:     cancel,
	}
	// Start the directory watcher with proper shutdown support
	rm.watcherWg.Add(1)
	go rm.watchRecordingsDir()
	return rm
}

// startRecordingPlaceholder checks for duplicates and creates a placeholder recording entry.
func (rm *RecordingManager) startRecordingPlaceholder(name, sourceURL string) (string, *Recording, error) {
	rm.Logger.Debug("startRecordingPlaceholder called", "name", name, "sourceURL", sourceURL)
	rm.mu.Lock()
	// Check for existing active recordings by name and source
	for _, rec := range rm.recordings {
		if rec.Name == name && rec.Source == sourceURL && rec.Active {
			rm.mu.Unlock()
			rm.Logger.Warn("Active recording already exists", "name", name, "sourceURL", sourceURL)
			return "", nil, fmt.Errorf("active recording for name %s and source %s already exists", name, sourceURL)
		}
	}

	// Create a placeholder recording entry to prevent race conditions
	currentTime := time.Now()
	timestamp := currentTime.Unix()
	uniqueKey := fmt.Sprintf("%s_%d", name, timestamp) // Only use name and timestamp for uniqueKey
	placeholderRec := &Recording{
		Name:      name,
		Source:    sourceURL,
		StartedAt: currentTime,
		Active:    true, // Mark as active immediately to block other attempts
	}
	rm.recordings[uniqueKey] = placeholderRec
	rm.mu.Unlock()
	return uniqueKey, placeholderRec, nil
}

// startRecordingProcess starts the relay and ffmpeg process, handling errors and cleanup.
func (rm *RecordingManager) startRecordingProcess(name, uniqueKey string) (string, FFmpegProcess, context.CancelFunc, error) {
	rm.Logger.Debug("startRecordingProcess called", "name", name, "uniqueKey", uniqueKey)
	localRelayURL, err := rm.RelayMgr.StartInputRelayForConsumer(name)
	if err != nil {
		rm.Logger.Error("Failed to start input relay for recording", "err", err)
		rm.mu.Lock()
		delete(rm.recordings, uniqueKey)
		rm.mu.Unlock()
		return "", nil, nil, err
	}
	filePath := fmt.Sprintf("%s/%s.mp4", rm.dir, uniqueKey) // Filename is now name_timestamp.mp4
	rm.Logger.Debug("Starting ffmpeg for recording", "filePath", filePath, "name", name, "uniqueKey", uniqueKey, "localRelayURL", localRelayURL)
	ffmpegArgs := []string{"-y", "-i", localRelayURL, "-c", "copy", filePath}
	procCtx, procCancel := context.WithCancel(context.Background())
	proc, err := NewFFmpegProcess(procCtx, ffmpegArgs...)
	if err != nil {
		procCancel() // prevent context leak
		rm.Logger.Error("Failed to create ffmpeg process", "err", err)
		rm.RelayMgr.StopInputRelayForConsumer(name, "")
		rm.mu.Lock()
		delete(rm.recordings, uniqueKey)
		rm.mu.Unlock()
		return "", nil, nil, err
	}
	if err := proc.Start(procCtx); err != nil {
		procCancel() // prevent context leak
		rm.Logger.Error("Failed to start ffmpeg", "err", err)
		rm.RelayMgr.StopInputRelayForConsumer(name, "")
		rm.mu.Lock()
		delete(rm.recordings, uniqueKey)
		rm.mu.Unlock()
		return "", nil, nil, err
	}
	// Ownership of procCancel is transferred to the lifecycle goroutine
	return filePath, proc, procCancel, nil
}

// handleRecordingLifecycle runs the recording lifecycle goroutine.
func (rm *RecordingManager) handleRecordingLifecycle(name, uniqueKey string, proc FFmpegProcess, procCancel context.CancelFunc, done chan struct{}) {
	defer procCancel() // Ensure process context is canceled when lifecycle ends
	defer rm.RelayMgr.StopInputRelayForConsumer(name, "")
	cmdDone := make(chan error, 1)
	go func() { cmdDone <- proc.Wait() }()
	select {
	case err := <-cmdDone:
		var filePath string
		rm.mu.Lock()
		if r, ok := rm.recordings[uniqueKey]; ok {
			r.Active = false
			r.StoppedAt = time.Now()
			filePath = r.FilePath
			if info, statErr := os.Stat(r.FilePath); statErr == nil {
				r.FileSize = info.Size()
				rm.Logger.Debug("Updated file size for finished recording", "name", name, "fileSize", r.FileSize)
			} else {
				rm.Logger.Warn("Could not get file size for finished recording", "name", name, "err", statErr)
			}
		} else {
			filePath = "(unknown)"
		}
		rm.mu.Unlock()
		sseBroker.NotifyAll("update")
		if err != nil {
			ffmpegOutput := proc.GetOutput()
			rm.Logger.Debug("[DEBUG] handleRecordingLifecycle: ffmpegOutput length = %d", len(ffmpegOutput))
			rm.Logger.Error("ffmpeg exited with error", "name", name, "filePath", filePath, "err", err, "output", ffmpegOutput)
		} else {
			rm.Logger.Info("Recording finished", "name", name, "filePath", filePath)
		}
	case <-done:
		rm.Logger.Debug("StartRecording: recording goroutine done channel closed", "uniqueKey", uniqueKey)
		if proc.GetPID() != 0 {
			pid := proc.GetPID()
			rm.Logger.Info("RecordingManager: Gracefully terminating ffmpeg process", "pid", pid, "name", name)
			err := proc.Stop(context.Background(), 2*time.Second)
			if err != nil {
				rm.Logger.Warn("Failed to stop ffmpeg process", "pid", pid, "err", err)
			}
		}
		<-cmdDone
		rm.mu.Lock()
		if r, ok := rm.recordings[uniqueKey]; ok {
			r.Active = false
			r.StoppedAt = time.Now()
			if info, statErr := os.Stat(r.FilePath); statErr == nil {
				r.FileSize = info.Size()
				rm.Logger.Debug("Updated file size for stopped recording", "name", name, "fileSize", r.FileSize)
			} else {
				rm.Logger.Warn("Could not get file size for stopped recording", "name", name, "err", statErr)
			}
		}
		rm.mu.Unlock()
		sseBroker.NotifyAll("update")
	}
	// Cleanup
	rm.mu.Lock()
	delete(rm.processes, uniqueKey)
	delete(rm.dones, uniqueKey)
	rm.mu.Unlock()
}

func (rm *RecordingManager) StartRecording(ctx context.Context, name, sourceURL string) error {
	rm.Logger.Info("StartRecording called", "name", name, "source", sourceURL)
	uniqueKey, placeholderRec, err := rm.startRecordingPlaceholder(name, sourceURL)
	if err != nil {
		return err
	}
	filePath, proc, procCancel, err := rm.startRecordingProcess(name, uniqueKey)
	if err != nil {
		return err
	}
	// Protect all updates to shared state with the mutex
	rm.mu.Lock()
	placeholderRec.FilePath = filePath
	placeholderRec.Filename = filepath.Base(filePath)
	rm.processes[uniqueKey] = proc
	done := make(chan struct{})
	rm.dones[uniqueKey] = done
	rm.mu.Unlock()
	go rm.handleRecordingLifecycle(name, uniqueKey, proc, procCancel, done)
	sseBroker.NotifyAll("update")
	return nil
}

// StopRecording stops the latest active recording for a given name+source
func (rm *RecordingManager) StopRecording(name string, source string) error {
	rm.Logger.Info("StopRecording called", "name", name, "source", source)
	rm.mu.Lock()
	// Find the latest active recording for this name+source
	var latestKey string
	var latestTime int64
	for key, rec := range rm.recordings {
		if rec.Name == name && rec.Source == source && rec.Active {
			started := rec.StartedAt.Unix()
			if latestKey == "" || started > latestTime {
				latestKey = key
				latestTime = started
			}
		}
	}
	if latestKey == "" {
		rm.mu.Unlock()
		rm.Logger.Warn("No active recording found", "name", name, "source", source)
		return fmt.Errorf("no active recording with name %s and source %s", name, source)
	}
	done, ok := rm.dones[latestKey]
	if !ok {
		// Check if the recording is still active - if not, it likely finished naturally
		if rec, exists := rm.recordings[latestKey]; exists && !rec.Active {
			rm.mu.Unlock()
			rm.Logger.Info("Recording has already finished naturally", "name", name)
			// Trigger UI update since recording is already stopped
			sseBroker.NotifyAll("update")
			return nil // Not an error, just already finished
		}
		rm.mu.Unlock()
		rm.Logger.Info("Recording appears to have finished naturally (no done channel found)", "name", name)
		// Trigger UI update in case the recording finished but UI wasn't updated
		sseBroker.NotifyAll("update")
		return nil // Don't treat this as an error anymore
	}
	close(done)
	delete(rm.dones, latestKey)
	rm.mu.Unlock()
	rm.Logger.Info("Stopped recording", "name", name)
	return nil
}

// StopAllRecordings stops all active recordings gracefully
func (rm *RecordingManager) StopAllRecordings() {
	rm.Logger.Info("RecordingManager: Stopping all active recordings...")

	rm.mu.Lock()
	activeRecordings := make([]struct{ name, source string }, 0)
	for _, recording := range rm.recordings {
		if recording.Active {
			activeRecordings = append(activeRecordings, struct{ name, source string }{recording.Name, recording.Source})
		}
	}
	// Release lock before calling StopRecording to avoid deadlock
	rm.mu.Unlock()

	if len(activeRecordings) == 0 {
		rm.Logger.Info("RecordingManager: No active recordings to stop")
		return
	}

	// Stop each active recording
	for _, rec := range activeRecordings {
		rm.Logger.Info("RecordingManager: Stopping recording", "name", rec.name)
		if err := rm.StopRecording(rec.name, rec.source); err != nil {
			rm.Logger.Debug("RecordingManager: Stop recording result", "name", rec.name, "err", err)
		}
	}

	rm.Logger.Info("RecordingManager: All recordings stopped")
}

// Shutdown gracefully shuts down the RecordingManager
func (rm *RecordingManager) Shutdown() {
	rm.Logger.Info("RecordingManager: Shutting down...")

	// Stop all active recordings first
	rm.StopAllRecordings()

	// Shutdown SSE broker to close all active SSE connections
	rm.Logger.Debug("RecordingManager: Shutting down SSE broker...")
	sseBroker.Shutdown()

	// Cancel the context to signal the directory watcher to stop
	rm.cancel()

	// Wait for the directory watcher to exit
	rm.watcherWg.Wait()

	rm.Logger.Info("RecordingManager: Shutdown complete")
}

// ListRecordings returns all recordings
func (rm *RecordingManager) ListRecordings() []*Recording {
	rm.mu.RLock()
	recs := make([]*Recording, 0, len(rm.recordings))
	fileSet := make(map[string]struct{})
	for _, r := range rm.recordings {
		// Create a copy of the recording to avoid race conditions
		recCopy := &Recording{
			Name:      r.Name,
			Source:    r.Source,
			FilePath:  r.FilePath,
			Filename:  r.Filename,
			FileSize:  r.FileSize,
			StartedAt: r.StartedAt,
			StoppedAt: r.StoppedAt,
			Active:    r.Active,
		}

		// For active/in-process, update file size from disk
		if recCopy.Active && recCopy.FilePath != "" {
			if info, err := os.Stat(recCopy.FilePath); err == nil {
				recCopy.FileSize = info.Size()
			}
		} else if !recCopy.Active && recCopy.FilePath != "" && recCopy.FileSize == 0 {
			// For inactive recordings with zero file size, try to get actual size
			if info, err := os.Stat(recCopy.FilePath); err == nil {
				recCopy.FileSize = info.Size()
			}
		}
		recs = append(recs, recCopy)
		if recCopy.Filename != "" {
			fileSet[recCopy.Filename] = struct{}{}
		}
	}
	rm.mu.RUnlock()

	// Scan disk for .mp4 files in recordings dir
	files, err := os.ReadDir(rm.dir)
	if err == nil {
		for _, f := range files {
			if f.IsDir() || filepath.Ext(f.Name()) != ".mp4" {
				continue
			}
			if _, exists := fileSet[f.Name()]; exists {
				continue // skip duplicate
			}
			filePath := filepath.Join(rm.dir, f.Name())
			// Try to extract name from filename: <name>_<timestamp>.mp4
			base := f.Name()[:len(f.Name())-4] // strip .mp4
			sep := -1
			for i := len(base) - 1; i >= 0; i-- {
				if base[i] == '_' {
					sep = i
					break
				}
			}
			var name string
			if sep > 0 {
				name = base[:sep]
			} else {
				name = base
			}
			info, err := f.Info()
			started := time.Time{}
			var size int64
			if err == nil {
				started = info.ModTime()
				size = info.Size()
			}
			recs = append(recs, &Recording{
				Name:      name,
				Source:    "",
				FilePath:  filePath,
				Filename:  f.Name(),
				FileSize:  size,
				StartedAt: started,
				Active:    false,
			})
		}
	}
	return recs
}

// DeleteRecordingByFilename deletes a recording file by filename and removes from map if present
func (rm *RecordingManager) DeleteRecordingByFilename(filename string) error {
	rm.Logger.Info("DeleteRecordingByFilename called", "filename", filename)
	filePath := filepath.Join(rm.dir, filename)
	if err := os.Remove(filePath); err != nil {
		rm.Logger.Error("Failed to delete file", "filePath", filePath, "err", err)
		return err
	}
	rm.mu.Lock()
	for key, rec := range rm.recordings {
		if rec.Filename == filename {
			delete(rm.recordings, key)
			rm.Logger.Info("Deleted in-memory recording", "filename", filename, "key", key)
			break
		}
	}
	rm.mu.Unlock()
	rm.Logger.Info("Deleted recording file", "filePath", filePath)
	sseBroker.NotifyAll("update")
	return nil
}

// Helper to find last underscore (for extracting filename)
func lastUnderscore(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '_' {
			return i
		}
	}
	return -1
}

// SSEBroker manages Server-Sent Events clients for real-time UI updates
// This implements a fan-out pattern to broadcast updates to multiple browser clients
var sseBroker = &SSEBroker{
	clients:  make(map[chan string]struct{}),
	shutdown: make(chan struct{}),
}

// SSEBroker handles real-time communication with web browser clients
// It maintains a registry of active client connections and broadcasts updates
type SSEBroker struct {
	clients  map[chan string]struct{} // Map of active client channels
	mu       sync.Mutex               // Protects concurrent access to clients map
	shutdown chan struct{}            // Signals when broker should shut down
	once     sync.Once                // Ensures shutdown only happens once safely
}

// NotifyAll broadcasts a message to all connected SSE clients
// Uses non-blocking sends to prevent slow clients from blocking the broadcast
func (b *SSEBroker) NotifyAll(msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		select {
		case ch <- msg:
			// Message sent successfully
		default:
			// Client channel is full/blocked - skip to prevent blocking
			// This prevents slow clients from affecting other clients
		}
	}
}

// AddClient registers a new SSE client channel for receiving updates
func (b *SSEBroker) AddClient(ch chan string) {
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
}

// RemoveClient unregisters an SSE client channel
func (b *SSEBroker) RemoveClient(ch chan string) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
}

// Shutdown gracefully closes all active SSE connections
// Uses sync.Once to ensure it can only be called once safely
func (b *SSEBroker) Shutdown() {
	b.once.Do(func() {
		close(b.shutdown)
		b.mu.Lock()
		defer b.mu.Unlock()
		// Close all client channels to force connections to end gracefully
		for ch := range b.clients {
			close(ch)
		}
		// Clear clients map to prevent memory leaks
		b.clients = make(map[chan string]struct{})
	})
}

// SSE handler
func ApiRecordingsSSE() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		ch := make(chan string, 1)
		sseBroker.AddClient(ch)
		defer sseBroker.RemoveClient(ch)
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					// Channel was closed, exit gracefully
					return
				}
				w.Write([]byte("data: " + msg + "\n\n"))
				flusher.Flush()
			case <-r.Context().Done():
				return
			case <-sseBroker.shutdown:
				return
			}
		}
	}
}

// watchRecordingsDir watches for changes in the recordings directory and notifies via SSE
// This function uses inotify (Linux) to efficiently monitor filesystem events.
// It runs in its own goroutine and handles proper shutdown via context cancellation.
func (rm *RecordingManager) watchRecordingsDir() {
	defer rm.watcherWg.Done()
	rm.Logger.Debug("RecordingManager: Starting directory watcher for %s", rm.dir)

	// Initialize inotify file descriptor for filesystem event monitoring
	fd, err := unix.InotifyInit()
	if err != nil {
		rm.Logger.Error("RecordingManager: Failed to initialize inotify", "err", err)
		return
	}
	defer unix.Close(fd)

	// Add a watch for the recordings directory
	// Monitor file creation, modification, deletion, and moves
	wd, err := unix.InotifyAddWatch(fd, rm.dir, unix.IN_CREATE|unix.IN_MODIFY|unix.IN_DELETE|unix.IN_MOVED_FROM|unix.IN_MOVED_TO|unix.IN_CLOSE_WRITE)
	if err != nil {
		rm.Logger.Error("RecordingManager: Failed to add inotify watch", "err", err)
		return
	}
	defer unix.InotifyRmWatch(fd, uint32(wd))

	// Use separate goroutine for blocking inotify reads to enable graceful shutdown
	// This pattern allows us to select between inotify events and shutdown signals
	eventCh := make(chan []byte, 1)
	errCh := make(chan error, 1)

	go func() {
		buf := make([]byte, 4096) // Buffer for inotify events
		for {
			// Blocking read for inotify events
			n, err := unix.Read(fd, buf)
			if err != nil {
				select {
				case errCh <- err:
				case <-rm.ctx.Done():
					return
				}
				return
			}

			// Copy buffer data since it will be reused
			eventData := make([]byte, n)
			copy(eventData, buf[:n])

			select {
			case eventCh <- eventData:
			case <-rm.ctx.Done():
				return
			}
		}
	}()

	// Main event processing loop
	for {
		select {
		case <-rm.ctx.Done():
			rm.Logger.Debug("RecordingManager: Directory watcher shutting down")
			return
		case err := <-errCh:
			rm.Logger.Error("RecordingManager: Error reading inotify events", "err", err)
			return
		case eventData := <-eventCh:
			// Process inotify events from the buffer
			// Multiple events can be packed into a single read
			var offset uint32
			n := len(eventData)
			for offset <= uint32(n-unix.SizeofInotifyEvent) {
				// Parse each inotify event from the buffer
				raw := (*unix.InotifyEvent)(unsafe.Pointer(&eventData[offset]))
				mask := raw.Mask

				// Check if this is a relevant file system event
				if mask&(unix.IN_CREATE|unix.IN_MODIFY|unix.IN_DELETE|unix.IN_MOVED_FROM|unix.IN_MOVED_TO|unix.IN_CLOSE_WRITE) != 0 {
					// Notify all SSE clients that the recordings list should be updated
					sseBroker.NotifyAll("update")
				}

				// Move to next event in buffer
				offset += unix.SizeofInotifyEvent + raw.Len
			}
		}
	}
}
