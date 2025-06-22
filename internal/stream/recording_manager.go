package stream

import (
	"bytes"
	"context"
	"fmt"
	"go-mls/internal/logger"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Recording represents a recording session or file
type Recording struct {
	Name      string    `json:"name"`
	Source    string    `json:"source"`
	FilePath  string    `json:"file_path"`
	Filename  string    `json:"filename"`
	FileSize  int64     `json:"file_size"`
	StartedAt time.Time `json:"started_at"`
	StoppedAt time.Time `json:"stopped_at,omitempty"`
	Active    bool      `json:"active"`
}

// RecordingManager manages active and completed recordings
// Now uses RelayManager for local relay and refcounting
type RecordingManager struct {
	mu         sync.Mutex
	recordings map[string]*Recording
	processes  map[string]*exec.Cmd
	dones      map[string]chan struct{} // done channel for each recording
	Logger     *logger.Logger           // Add logger field
	dir        string                   // Recordings directory
	RelayMgr   *RelayManager            // Reference to RelayManager for local relay

	// Shutdown support
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
		processes:  make(map[string]*exec.Cmd),
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

// StartRecording starts recording a source to a file using ffmpeg, using local relay URL
func (rm *RecordingManager) StartRecording(ctx context.Context, name, sourceURL string) error {
	rm.Logger.Info("StartRecording called: name=%s, source=%s", name, sourceURL)
	
	// Use a more robust approach to prevent duplicate recordings
	// Create a deterministic key for the recording based on name and source
	recordingKey := fmt.Sprintf("%s_%s", name, sourceURL)
	
	rm.mu.Lock()
	// Check for existing active recordings by name and source
	for _, rec := range rm.recordings {
		if rec.Name == name && rec.Source == sourceURL && rec.Active {
			rm.mu.Unlock()
			rm.Logger.Warn("Active recording for name %s and source %s already exists", name, sourceURL)
			return fmt.Errorf("active recording for name %s and source %s already exists", name, sourceURL)
		}
	}
	
	// Create a placeholder recording entry to prevent race conditions
	currentTime := time.Now()
	timestamp := currentTime.Unix()
	uniqueKey := fmt.Sprintf("%s_%d", recordingKey, timestamp)
	placeholderRec := &Recording{
		Name:      name,
		Source:    sourceURL,
		StartedAt: currentTime,
		Active:    true, // Mark as active immediately
	}
	rm.recordings[uniqueKey] = placeholderRec
	rm.mu.Unlock()

	// Compose local RTSP relay path and URL
	relayPath := fmt.Sprintf("relay/%s", name)
	localRelayURL := fmt.Sprintf("rtsp://127.0.0.1:8554/%s", relayPath) // or use GetRTSPServerURL if available
	inputTimeout := 30 * time.Second                                    // match relay_manager.go
	_, err := rm.RelayMgr.InputRelays.StartInputRelay(name, sourceURL, localRelayURL, inputTimeout)
	if err != nil {
		rm.Logger.Error("Failed to start input relay for recording: %v", err)
		// Clean up the placeholder recording entry
		rm.mu.Lock()
		delete(rm.recordings, uniqueKey)
		rm.mu.Unlock()
		return err
	}

	// Double-check logic is no longer needed since we have a placeholder entry
	
	// Wait for the RTSP stream to become ready before starting recording ffmpeg
	rtspServer := rm.RelayMgr.GetRTSPServer()
	if rtspServer != nil {
		rm.Logger.Info("Waiting for RTSP stream to become ready for recording: %s", relayPath)
		err = rtspServer.WaitForStreamReady(relayPath, 30*time.Second)
		if err != nil {
			rm.Logger.Error("Failed to wait for RTSP stream to become ready for recording %s: %v", name, err)
			rm.Logger.Debug("Stream readiness check failed for %s, checking if stream exists...", relayPath)
			if rtspServer.IsStreamReady(relayPath) {
				rm.Logger.Warn("Stream %s appears ready but wait failed, continuing anyway", relayPath)
			} else {
				rm.RelayMgr.InputRelays.StopInputRelay(sourceURL)
				// Clean up the placeholder recording entry
				rm.mu.Lock()
				delete(rm.recordings, uniqueKey)
				rm.mu.Unlock()
				return fmt.Errorf("RTSP stream not ready for recording: %v", err)
			}
		}
		rm.Logger.Info("RTSP stream is ready for recording: %s", relayPath)
	}

	rm.mu.Lock()
	defer rm.mu.Unlock()

	filePath := fmt.Sprintf("%s/%s_%d.mp4", rm.dir, name, timestamp)
	rm.Logger.Debug("Starting ffmpeg for recording: %s", filePath)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", localRelayURL, "-c", "copy", filePath)
	rm.Logger.Debug("RecordingManager: Starting ffmpeg command: %v", cmd.Args)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		rm.Logger.Error("Failed to start ffmpeg: %v", err)
		rm.RelayMgr.InputRelays.StopInputRelay(sourceURL)
		// Clean up the placeholder recording entry
		delete(rm.recordings, uniqueKey)
		return err
	}
	rm.Logger.Info("RecordingManager: Started ffmpeg process PID %d for recording %s", cmd.Process.Pid, filePath)
	
	// Update the placeholder recording with actual file information
	placeholderRec.FilePath = filePath
	placeholderRec.Filename = fmt.Sprintf("%s_%d.mp4", name, timestamp)
	rm.processes[uniqueKey] = cmd
	done := make(chan struct{})
	rm.dones[uniqueKey] = done
	go func(key string, done chan struct{}) {
		defer rm.RelayMgr.InputRelays.StopInputRelay(sourceURL) // Decrement relay when recording ends
		select {
		case err := <-waitCmd(cmd):
			var filePath string
			rm.mu.Lock()
			if r, ok := rm.recordings[key]; ok {
				r.Active = false
				r.StoppedAt = time.Now()
				filePath = r.FilePath
			} else {
				filePath = "(unknown)"
			}
			rm.mu.Unlock()
			sseBroker.NotifyAll("update")
			if err != nil {
				ffmpegOut := outBuf.String()
				ffmpegErr := errBuf.String()
				rm.Logger.Error("ffmpeg exited with error for %s (%s): %v\nstdout:\n%s\nstderr:\n%s", name, filePath, err, ffmpegOut, ffmpegErr)
			} else {
				rm.Logger.Info("Recording finished for %s (%s)", name, filePath)
			}
		case <-done:
			// Stop requested
			rm.Logger.Debug("Recording for %s stopped by done channel.", name)
			// Send SIGINT for graceful shutdown
			if cmd.Process != nil {
				pid := cmd.Process.Pid
				rm.Logger.Info("RecordingManager: Gracefully terminating ffmpeg process PID %d for recording %s", pid, name)
				err := cmd.Process.Signal(syscall.SIGINT)
				if err != nil {
					rm.Logger.Warn("Failed to send SIGINT to ffmpeg process PID %d: %v", pid, err)
				}
			}
			// Wait for ffmpeg to exit and finalize file
			_ = cmd.Wait()
			// No need for filePath here, already handled below
			rm.mu.Lock()
			if r, ok := rm.recordings[key]; ok {
				r.Active = false
				r.StoppedAt = time.Now()
			}
			rm.mu.Unlock()
			sseBroker.NotifyAll("update")
		}
		// Cleanup
		rm.mu.Lock()
		delete(rm.processes, key)
		delete(rm.dones, key)
		rm.mu.Unlock()
	}(uniqueKey, done)
	sseBroker.NotifyAll("update")
	return nil
}

// Helper to wait for cmd.Wait() in a channel
func waitCmd(cmd *exec.Cmd) <-chan error {
	ch := make(chan error, 1)
	go func() {
		ch <- cmd.Wait()
	}()
	return ch
}

// StopRecording stops the latest active recording for a given name+source
func (rm *RecordingManager) StopRecording(name string, source string) error {
	rm.Logger.Info("StopRecording called: name=%s, source=%s", name, source)
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
		rm.Logger.Warn("No active recording with name %s and source %s", name, source)
		return fmt.Errorf("no active recording with name %s and source %s", name, source)
	}
	done, ok := rm.dones[latestKey]
	if !ok {
		// Check if the recording is still active - if not, it likely finished naturally
		if rec, exists := rm.recordings[latestKey]; exists && !rec.Active {
			rm.mu.Unlock()
			rm.Logger.Info("Recording for %s has already finished naturally", name)
			// Trigger UI update since recording is already stopped
			sseBroker.NotifyAll("update")
			return nil // Not an error, just already finished
		}
		rm.mu.Unlock()
		rm.Logger.Info("Recording for %s appears to have finished naturally (no done channel found)", name)
		// Trigger UI update in case the recording finished but UI wasn't updated
		sseBroker.NotifyAll("update")
		return nil // Don't treat this as an error anymore
	}
	close(done)
	delete(rm.dones, latestKey)
	rm.mu.Unlock()
	rm.Logger.Info("Stopped recording for %s", name)
	return nil
}

// StopAllRecordings stops all active recordings gracefully
func (rm *RecordingManager) StopAllRecordings() {
	rm.Logger.Info("RecordingManager: Stopping all active recordings...")

	rm.mu.Lock()
	activeRecordings := make([]string, 0)
	for name, recording := range rm.recordings {
		if recording.Active {
			activeRecordings = append(activeRecordings, name)
		}
	}
	rm.mu.Unlock()

	// Stop each active recording
	for _, name := range activeRecordings {
		rm.mu.Lock()
		recording := rm.recordings[name]
		var source string
		if recording != nil {
			source = recording.Source
		}
		rm.mu.Unlock()

		rm.Logger.Info("RecordingManager: Stopping recording %s", name)
		if err := rm.StopRecording(name, source); err != nil {
			rm.Logger.Error("RecordingManager: Failed to stop recording %s: %v", name, err)
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
	rm.mu.Lock()
	recs := make([]*Recording, 0, len(rm.recordings))
	fileSet := make(map[string]struct{})
	for _, r := range rm.recordings {
		// For active/in-process, update file size from disk
		if r.Active && r.FilePath != "" {
			if info, err := os.Stat(r.FilePath); err == nil {
				r.FileSize = info.Size()
			}
		}
		recs = append(recs, r)
		if r.Filename != "" {
			fileSet[r.Filename] = struct{}{}
		}
	}
	rm.mu.Unlock()

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

// DeleteRecording removes a recording file and metadata
func (rm *RecordingManager) DeleteRecording(key string) error {
	rm.Logger.Info("DeleteRecording called: key=%s", key)
	rm.mu.Lock()
	r, ok := rm.recordings[key]
	rm.mu.Unlock()
	if ok {
		rm.mu.Lock()
		if r.Active {
			rm.mu.Unlock()
			rm.Logger.Warn("Cannot delete active recording: %s", key)
			return fmt.Errorf("cannot delete active recording")
		}
		if err := exec.Command("rm", "-f", r.FilePath).Run(); err != nil {
			rm.Logger.Error("Failed to delete file %s: %v", r.FilePath, err)
			rm.mu.Unlock()
			return err
		}
		delete(rm.recordings, key)
		rm.Logger.Info("Deleted recording %s", key)
		rm.mu.Unlock()
		sseBroker.NotifyAll("update")
		return nil
	}
	// Fallback: try to delete by filename for on-disk-only recordings
	filename := key + ".mp4"
	filePath := filepath.Join(rm.dir, filename)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		// Try single-underscore variant if double-underscore does not exist
		if idx := lastUnderscore(key); idx > 0 && key[idx-1] == '_' {
			altFilename := key[:idx-1] + key[idx:] + ".mp4"
			altFilePath := filepath.Join(rm.dir, altFilename)
			if _, err2 := os.Stat(altFilePath); err2 == nil {
				filePath = altFilePath
			}
		}
	}
	if err := exec.Command("rm", "-f", filePath).Run(); err != nil {
		rm.Logger.Error("Failed to delete file %s: %v", filePath, err)
		return err
	}
	rm.Logger.Info("Deleted on-disk-only recording %s", filePath)
	sseBroker.NotifyAll("update")
	return nil
}

// DeleteRecordingByFilename deletes a recording file by filename and removes from map if present
func (rm *RecordingManager) DeleteRecordingByFilename(filename string) error {
	rm.Logger.Info("DeleteRecordingByFilename called: filename=%s", filename)
	filePath := filepath.Join(rm.dir, filename)
	if err := exec.Command("rm", "-f", filePath).Run(); err != nil {
		rm.Logger.Error("Failed to delete file %s: %v", filePath, err)
		return err
	}
	rm.mu.Lock()
	for key, rec := range rm.recordings {
		if rec.Filename == filename {
			delete(rm.recordings, key)
			rm.Logger.Info("Deleted in-memory recording %s (key=%s)", filename, key)
			break
		}
	}
	rm.mu.Unlock()
	rm.Logger.Info("Deleted recording file %s", filePath)
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

// SSEBroker manages Server-Sent Events clients
var sseBroker = &SSEBroker{
	clients:  make(map[chan string]struct{}),
	shutdown: make(chan struct{}),
}

type SSEBroker struct {
	clients  map[chan string]struct{}
	mu       sync.Mutex
	shutdown chan struct{}
	once     sync.Once
}

func (b *SSEBroker) NotifyAll(msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (b *SSEBroker) AddClient(ch chan string) {
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
}

func (b *SSEBroker) RemoveClient(ch chan string) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
}

// Shutdown closes all active SSE connections
func (b *SSEBroker) Shutdown() {
	b.once.Do(func() {
		close(b.shutdown)
		b.mu.Lock()
		defer b.mu.Unlock()
		// Close all client channels to force connections to end
		for ch := range b.clients {
			close(ch)
		}
		// Clear clients map
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
func (rm *RecordingManager) watchRecordingsDir() {
	defer rm.watcherWg.Done()
	rm.Logger.Debug("RecordingManager: Starting directory watcher for %s", rm.dir)

	fd, err := unix.InotifyInit()
	if err != nil {
		rm.Logger.Error("RecordingManager: Failed to initialize inotify: %v", err)
		return
	}
	defer unix.Close(fd)

	wd, err := unix.InotifyAddWatch(fd, rm.dir, unix.IN_CREATE|unix.IN_MODIFY|unix.IN_DELETE|unix.IN_MOVED_FROM|unix.IN_MOVED_TO|unix.IN_CLOSE_WRITE)
	if err != nil {
		rm.Logger.Error("RecordingManager: Failed to add inotify watch: %v", err)
		return
	}
	defer unix.InotifyRmWatch(fd, uint32(wd))

	// Use a goroutine to handle the blocking read and communicate via channels
	eventCh := make(chan []byte, 1)
	errCh := make(chan error, 1)

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := unix.Read(fd, buf)
			if err != nil {
				select {
				case errCh <- err:
				case <-rm.ctx.Done():
					return
				}
				return
			}

			// Make a copy of the buffer to send
			eventData := make([]byte, n)
			copy(eventData, buf[:n])

			select {
			case eventCh <- eventData:
			case <-rm.ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-rm.ctx.Done():
			rm.Logger.Debug("RecordingManager: Directory watcher shutting down")
			return
		case err := <-errCh:
			rm.Logger.Error("RecordingManager: Error reading inotify events: %v", err)
			return
		case eventData := <-eventCh:
			// Process the events
			var offset uint32
			n := len(eventData)
			for offset <= uint32(n-unix.SizeofInotifyEvent) {
				raw := (*unix.InotifyEvent)(unsafe.Pointer(&eventData[offset]))
				mask := raw.Mask
				if mask&(unix.IN_CREATE|unix.IN_MODIFY|unix.IN_DELETE|unix.IN_MOVED_FROM|unix.IN_MOVED_TO|unix.IN_CLOSE_WRITE) != 0 {
					sseBroker.NotifyAll("update")
				}
				offset += unix.SizeofInotifyEvent + raw.Len
			}
		}
	}
}
