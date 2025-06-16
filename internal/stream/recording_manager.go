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
type RecordingManager struct {
	mu         sync.Mutex
	recordings map[string]*Recording
	processes  map[string]*exec.Cmd
	dones      map[string]chan struct{} // done channel for each recording
	Logger     *logger.Logger           // Add logger field
	dir        string                   // Recordings directory
}

// NewRecordingManager creates a RecordingManager and ensures the directory exists
func NewRecordingManager(l *logger.Logger, dir string) *RecordingManager {
	if err := os.MkdirAll(dir, 0755); err != nil {
		panic(fmt.Sprintf("Failed to create recordings directory: %v", err))
	}
	go watchRecordingsDir(dir)
	return &RecordingManager{
		recordings: make(map[string]*Recording),
		processes:  make(map[string]*exec.Cmd),
		dones:      make(map[string]chan struct{}),
		Logger:     l,
		dir:        dir,
	}
}

// StartRecording starts recording a source to a file using ffmpeg
func (rm *RecordingManager) StartRecording(ctx context.Context, name, sourceURL string) error {
	rm.Logger.Info("StartRecording called: name=%s, source=%s", name, sourceURL)
	rm.mu.Lock()
	defer rm.mu.Unlock()
	// Prevent multiple active recordings for the same input (name+source)
	for _, rec := range rm.recordings {
		if rec.Name == name && rec.Source == sourceURL && rec.Active {
			rm.Logger.Warn("Active recording for name %s and source %s already exists", name, sourceURL)
			return fmt.Errorf("active recording for name %s and source %s already exists", name, sourceURL)
		}
	}
	filePath := fmt.Sprintf("%s/%s_%d.mp4", rm.dir, name, time.Now().Unix())
	rm.Logger.Debug("Starting ffmpeg for recording: %s", filePath)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", sourceURL, "-c", "copy", filePath)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		rm.Logger.Error("Failed to start ffmpeg: %v", err)
		return err
	}
	timestamp := time.Now().Unix()
	rec := &Recording{
		Name:      name,
		Source:    sourceURL,
		FilePath:  filePath,
		Filename:  fmt.Sprintf("%s_%d.mp4", name, timestamp),
		StartedAt: time.Now(),
		Active:    true,
	}
	key := fmt.Sprintf("%s_%s_%d", name, sourceURL, timestamp)
	rm.recordings[key] = rec
	rm.processes[key] = cmd
	done := make(chan struct{})
	rm.dones[key] = done
	go func(key string, done chan struct{}) {
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
			_ = cmd.Process.Kill()
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
	}(key, done)
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
		rm.mu.Unlock()
		rm.Logger.Warn("No done channel for active recording with name %s and source %s", name, source)
		return fmt.Errorf("no done channel for active recording with name %s and source %s", name, source)
	}
	close(done)
	delete(rm.dones, latestKey)
	rm.mu.Unlock()
	rm.Logger.Info("Stopped recording for %s", name)
	return nil
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
	clients: make(map[chan string]struct{}),
}

type SSEBroker struct {
	clients map[chan string]struct{}
	mu      sync.Mutex
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
			case msg := <-ch:
				w.Write([]byte("data: " + msg + "\n\n"))
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
}

// Refactor watcher to accept dir
func watchRecordingsDir(dir string) {
	fd, err := unix.InotifyInit()
	if err != nil {
		return
	}
	defer unix.Close(fd)
	wd, err := unix.InotifyAddWatch(fd, dir, unix.IN_CREATE|unix.IN_MODIFY|unix.IN_DELETE|unix.IN_MOVED_FROM|unix.IN_MOVED_TO|unix.IN_CLOSE_WRITE)
	if err != nil {
		return
	}
	defer unix.InotifyRmWatch(fd, uint32(wd))
	buf := make([]byte, 4096)
	for {
		n, err := unix.Read(fd, buf)
		if err != nil {
			return
		}
		var offset uint32
		for offset <= uint32(n-unix.SizeofInotifyEvent) {
			raw := (*unix.InotifyEvent)(unsafe.Pointer(&buf[offset]))
			mask := raw.Mask
			if mask&(unix.IN_CREATE|unix.IN_MODIFY|unix.IN_DELETE|unix.IN_MOVED_FROM|unix.IN_MOVED_TO|unix.IN_CLOSE_WRITE) != 0 {
				sseBroker.NotifyAll("update")
			}
			offset += unix.SizeofInotifyEvent + raw.Len
		}
	}
}
