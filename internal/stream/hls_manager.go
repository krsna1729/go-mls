package stream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type HLSSession struct {
	// Immutable fields (set at creation, never change)
	InputName  string
	LocalURL   string
	Dir        string
	IsConsumer bool // Whether this session is registered as an input relay consumer

	// --- Concurrency: mutable fields below are protected by HLSManager.mu ---
	ViewerIDs  map[string]time.Time // Track individual viewers with heartbeat
	LastAccess time.Time            // Last time any viewer accessed this session

	// --- Process management (concurrent-safe via FFmpegProcess) ---
	Proc *FFmpegProcess // FFmpeg process abstraction (handles concurrency and output capture)

	// --- Readiness flag (protected by ReadyMu) ---
	Ready   bool
	ReadyMu sync.RWMutex // Protects Ready flag
}

type HLSManager struct {
	// --- Mutable fields protected by mu ---
	sessions         map[string]*HLSSession
	failedInputs     map[string]time.Time // Track failed input attempts for cooldown
	notFoundLogTimes map[string]time.Time // Last log time for missing inputName warnings

	// --- Immutable/config fields (set at construction) ---
	cleanupInterval     time.Duration
	sessionTimeout      time.Duration
	ffmpegPath          string
	relayManager        *RelayManager // Reference to relay manager for consumer management
	failedCooldown      time.Duration // How long to block repeated attempts
	notFoundLogInterval time.Duration // Minimum interval between logs per inputName

	// --- Shutdown support ---
	ctx    context.Context    // Context for cancellation
	cancel context.CancelFunc // Cancel function for shutdown

	mu sync.Mutex // Protects all mutable fields above
}

func NewHLSManager(ffmpegPath string, cleanupInterval, sessionTimeout time.Duration) *HLSManager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &HLSManager{
		sessions:            make(map[string]*HLSSession),
		cleanupInterval:     cleanupInterval,
		sessionTimeout:      sessionTimeout,
		ffmpegPath:          ffmpegPath,
		relayManager:        nil, // Will be set later via SetRelayManager
		failedInputs:        make(map[string]time.Time),
		failedCooldown:      30 * time.Second, // Default cooldown for failed inputs
		notFoundLogTimes:    make(map[string]time.Time),
		notFoundLogInterval: 10 * time.Second, // Log at most once per 10s per inputName
		ctx:                 ctx,
		cancel:              cancel,
	}
	go m.cleanupLoop(ctx)
	return m
}

// SetRelayManager sets the relay manager reference for consumer management
func (m *HLSManager) SetRelayManager(rm *RelayManager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.relayManager = rm
}

// Start or get an HLS session for the given input
func (m *HLSManager) GetOrStartSession(inputName, localURL string) (*HLSSession, error) {
	m.mu.Lock()
	// Check for recent failure
	if failTime, failed := m.failedInputs[inputName]; failed {
		if time.Since(failTime) < m.failedCooldown {
			m.mu.Unlock()
			if m.relayManager != nil && m.relayManager.Logger != nil {
				m.relayManager.Logger.Warn("Input %s is in failed cooldown, refusing to start session", inputName)
			}
			return nil, errors.New("input unavailable (cooldown)")
		} else {
			// Cooldown expired, remove
			delete(m.failedInputs, inputName)
		}
	}
	defer m.mu.Unlock()

	if m.relayManager != nil && m.relayManager.Logger != nil {
		m.relayManager.Logger.Debug("GetOrStartSession: inputName=%s", inputName)
	}

	// Validate inputName (no path traversal)
	if strings.Contains(inputName, "..") || strings.ContainsAny(inputName, "/\\") {
		if m.relayManager != nil && m.relayManager.Logger != nil {
			m.relayManager.Logger.Error("Invalid input name: %s", inputName)
		}
		return nil, errors.New("invalid input name")
	}

	sess, exists := m.sessions[inputName]
	if exists {
		sess.LastAccess = time.Now()
		return sess, nil
	}

	// Start input relay as a consumer if relay manager is available
	var actualLocalURL string
	var err error
	if m.relayManager != nil {
		actualLocalURL, err = m.relayManager.StartInputRelayForConsumer(inputName)
		if err != nil {
			m.relayManager.Logger.Error("Failed to start input relay for HLS: %v", err)
			return nil, fmt.Errorf("failed to start input relay for HLS: %w", err)
		}
		time.Sleep(1 * time.Second)
		if _, found := m.relayManager.InputRelays.FindLocalURLByInputName(inputName); !found {
			m.relayManager.StopInputRelayForConsumer(inputName)
			m.relayManager.Logger.Error("Input relay failed to start for %s", inputName)
			return nil, fmt.Errorf("input relay failed to start for %s", inputName)
		}
	} else {
		actualLocalURL = localURL
	}

	dir, err := os.MkdirTemp("", "hls_"+inputName+"_")
	if err != nil {
		if m.relayManager != nil {
			m.relayManager.StopInputRelayForConsumer(inputName)
		}
		if m.relayManager != nil && m.relayManager.Logger != nil {
			m.relayManager.Logger.Error("Failed to create temp dir: %v", err)
		}
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	playlist := filepath.Join(dir, "index.m3u8")
	segmentPattern := filepath.Join(dir, "segment_%03d.ts")

	// Build ffmpeg args
	ffmpegArgs := []string{
		"-rtsp_transport", "tcp",
		"-analyzeduration", "500k",
		"-probesize", "500k",
		"-fflags", "nobuffer",
		"-i", actualLocalURL,
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-c:a", "aac",
		"-ac", "2",
		"-ar", "44100",
		"-f", "hls",
		"-hls_time", "2",
		"-hls_list_size", "6",
		"-hls_flags", "delete_segments+append_list",
		"-hls_segment_filename", segmentPattern,
		"-y",
		playlist,
	}

	procCtx, procCancel := context.WithCancel(context.Background())
	defer func() {
		if procCancel != nil {
			procCancel()
		}
	}()
	proc, err := NewFFmpegProcess(procCtx, ffmpegArgs...)
	if err != nil {
		os.RemoveAll(dir)
		if m.relayManager != nil {
			m.relayManager.StopInputRelayForConsumer(inputName)
		}
		return nil, fmt.Errorf("failed to create ffmpeg process: %w", err)
	}

	if err := proc.Start(); err != nil {
		os.RemoveAll(dir)
		if m.relayManager != nil {
			m.relayManager.StopInputRelayForConsumer(inputName)
		}
		return nil, fmt.Errorf("failed to start ffmpeg: %w", err)
	}
	procCancel = nil // Ownership transferred to process

	sess = &HLSSession{
		InputName:  inputName,
		LocalURL:   actualLocalURL,
		Dir:        dir,
		IsConsumer: m.relayManager != nil,
		ViewerIDs:  make(map[string]time.Time),
		LastAccess: time.Now(),
		Proc:       proc,
		Ready:      false,
	}
	m.sessions[inputName] = sess

	if m.relayManager != nil && m.relayManager.Logger != nil {
		m.relayManager.Logger.Info("Created new HLS session for inputName=%s", inputName)
	}

	// Start a goroutine to monitor ffmpeg startup and set Ready flag
	go func() {
		playlistPath := filepath.Join(sess.Dir, "index.m3u8")
		ready := false
		watcher, err := fsnotify.NewWatcher()
		if err == nil {
			defer watcher.Close()
			_ = watcher.Add(sess.Dir)
			timeout := time.After(10 * time.Second)
		outer:
			for !ready {
				// Check if file is already ready (handles race)
				if fi, err := os.Stat(playlistPath); err == nil && fi.Size() > 0 {
					ready = true
					break outer
				}
				select {
				case event := <-watcher.Events:
					if event.Name == playlistPath && (event.Op&fsnotify.Create != 0 || event.Op&fsnotify.Write != 0) {
						if fi, err := os.Stat(playlistPath); err == nil && fi.Size() > 0 {
							ready = true
							break outer
						}
					}
				case <-timeout:
					break outer
				case <-time.After(50 * time.Millisecond):
					// continue
				}
			}
		}
		if !ready {
			// Fallback to polling if fsnotify fails or times out
			for i := 0; i < 50; i++ {
				fileInfo, err := os.Stat(playlistPath)
				if err == nil && fileInfo.Size() > 0 {
					ready = true
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
		}
		if ready {
			sess.ReadyMu.Lock()
			sess.Ready = true
			sess.ReadyMu.Unlock()
			if m.relayManager != nil && m.relayManager.Logger != nil {
				m.relayManager.Logger.Info("HLS session ready for inputName=%s (fsnotify/poll)", inputName)
			}
			return
		}
		// If we get here, ffmpeg failed to create a usable playlist
		sess.ReadyMu.Lock()
		sess.Ready = false
		sess.ReadyMu.Unlock()
		if m.relayManager != nil && m.relayManager.Logger != nil {
			m.relayManager.Logger.Error("HLS session failed to become ready for inputName=%s", inputName)
			// Log last 10 lines of ffmpeg output for debugging
			if sess.Proc != nil {
				lines := sess.Proc.GetLastOutputLines(10)
				for _, line := range lines {
					if line != "" {
						m.relayManager.Logger.Error("ffmpeg output: %s", line)
					}
				}
			}
		}
	}()

	return sess, nil
}

// AddViewer adds a new viewer to the session and returns a viewer ID
func (m *HLSManager) AddViewer(inputName, localURL string) (string, error) {
	sess, err := m.GetOrStartSession(inputName, localURL)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Generate unique viewer ID
	viewerID := fmt.Sprintf("viewer_%d_%s", time.Now().UnixNano(), inputName)

	sess.ViewerIDs[viewerID] = time.Now()
	sess.LastAccess = time.Now()

	if m.relayManager != nil && m.relayManager.Logger != nil {
		m.relayManager.Logger.Info("Added viewer %s to inputName=%s", viewerID, inputName)
	}

	return viewerID, nil
}

// UpdateViewerHeartbeat updates the heartbeat for a viewer
func (m *HLSManager) UpdateViewerHeartbeat(inputName, viewerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if sess, exists := m.sessions[inputName]; exists {
		if _, viewerExists := sess.ViewerIDs[viewerID]; viewerExists {
			sess.ViewerIDs[viewerID] = time.Now()
			sess.LastAccess = time.Now()
		}
	}
}

// RemoveViewer removes a viewer from the session
func (m *HLSManager) RemoveViewer(inputName, viewerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if sess, exists := m.sessions[inputName]; exists {
		if _, viewerExists := sess.ViewerIDs[viewerID]; viewerExists {
			delete(sess.ViewerIDs, viewerID)
			if m.relayManager != nil && m.relayManager.Logger != nil {
				m.relayManager.Logger.Info("Removed viewer %s from inputName=%s", viewerID, inputName)
			}
			if len(sess.ViewerIDs) == 0 {
				sess.LastAccess = time.Now().Add(-m.sessionTimeout + 30*time.Second)
			}
		}
	}
}

// Shutdown gracefully stops the cleanup loop and cleans up all sessions and ffmpeg processes.
func (m *HLSManager) Shutdown() {
	m.cancel()
	var sessions []*HLSSession
	m.mu.Lock()
	for _, sess := range m.sessions {
		sessions = append(sessions, sess)
	}
	m.sessions = make(map[string]*HLSSession)
	m.mu.Unlock()

	for _, sess := range sessions {
		if sess.IsConsumer && m.relayManager != nil {
			m.relayManager.StopInputRelayForConsumer(sess.InputName)
		}
		if sess.Proc != nil {
			err := sess.Proc.Stop(2 * time.Second)
			if err != nil {
				if m.relayManager != nil && m.relayManager.Logger != nil {
					m.relayManager.Logger.Warn("Error stopping ffmpeg process for HLS session inputName=%s: %v", sess.InputName, err)
				}
			}
			sess.Proc.Wait()
		}
		os.RemoveAll(sess.Dir)
		if m.relayManager != nil && m.relayManager.Logger != nil {
			m.relayManager.Logger.Info("Cleaned up HLS session for inputName=%s (shutdown)", sess.InputName)
		}
	}
}

// ServeHLS serves HLS playlist or segment, concurrency-safe and with detailed logging
func (m *HLSManager) ServeHLS(w http.ResponseWriter, r *http.Request, inputName, file string, localURL string) {
	if m.relayManager != nil && m.relayManager.Logger != nil {
		m.relayManager.Logger.Debug("ServeHLS: inputName=%s, file=%s", inputName, file)
	}

	// --- Stale viewer check ---
	viewerID := r.URL.Query().Get("viewerID")
	if viewerID != "" {
		m.mu.Lock()
		sess, exists := m.sessions[inputName]
		if !exists {
			m.mu.Unlock()
			if m.relayManager != nil && m.relayManager.Logger != nil {
				m.relayManager.Logger.Warn("ServeHLS: inputName=%s not found for viewerID=%s", inputName, viewerID)
			}
			http.Error(w, "HLS session not found", http.StatusNotFound)
			return
		}
		last, ok := sess.ViewerIDs[viewerID]
		if !ok || time.Since(last) > 30*time.Second {
			// Remove stale viewer
			delete(sess.ViewerIDs, viewerID)
			if m.relayManager != nil && m.relayManager.Logger != nil {
				m.relayManager.Logger.Warn("Stale or missing viewerID %s for inputName=%s; denying request", viewerID, inputName)
			}
			m.mu.Unlock()
			http.Error(w, "Viewer session expired or invalid", http.StatusGone)
			return
		}
		// Update heartbeat
		sess.ViewerIDs[viewerID] = time.Now()
		sess.LastAccess = time.Now()
		m.mu.Unlock()
	}

	m.mu.Lock()
	sess, exists := m.sessions[inputName]
	// --- Rate limit 'inputName not found' log spam ---
	if !exists {
		now := time.Now()
		lastLog, ok := m.notFoundLogTimes[inputName]
		if !ok || now.Sub(lastLog) > m.notFoundLogInterval {
			if m.relayManager != nil && m.relayManager.Logger != nil {
				m.relayManager.Logger.Warn("ServeHLS: inputName=%s not found (no session)", inputName)
			}
			// Update last log time
			m.notFoundLogTimes[inputName] = now
		}
		m.mu.Unlock()
		http.Error(w, "HLS session not found", http.StatusNotFound)
		return
	}
	m.mu.Unlock()

	// Wait for session readiness with context cancellation
	ready := func() bool {
		sess.ReadyMu.RLock()
		defer sess.ReadyMu.RUnlock()
		return sess.Ready
	}
	waitCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	for !ready() {
		select {
		case <-waitCtx.Done():
			if m.relayManager != nil && m.relayManager.Logger != nil {
				m.relayManager.Logger.Error("HLS session not ready for inputName=%s", inputName)
			}
			http.Error(w, "HLS session not ready yet, please try again", http.StatusServiceUnavailable)
			return
		default:
			time.Sleep(200 * time.Millisecond)
		}
	}

	m.mu.Lock()
	sess.LastAccess = time.Now()
	m.mu.Unlock()

	path := filepath.Join(sess.Dir, file)

	// Set CORS headers for browser compatibility
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Handle preflight requests
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// For playlist requests, do a final check that file exists
	if strings.HasSuffix(file, ".m3u8") {
		// Check if the file exists and is readable
		fileInfo, statErr := os.Stat(path)
		if statErr != nil {
			if m.relayManager != nil && m.relayManager.Logger != nil {
				m.relayManager.Logger.Error("HLS playlist not available: %v", statErr)
			}
			http.Error(w, "HLS playlist not available: "+statErr.Error(), http.StatusNotFound)
			return
		}

		// Ensure the file has proper permissions
		if fileInfo.Size() == 0 {
			// If the file exists but is empty, wait a bit for it to be populated
			time.Sleep(500 * time.Millisecond)
		}
		if m.relayManager != nil && m.relayManager.Logger != nil {
			m.relayManager.Logger.Debug("HLS playlist request: path=%s, size=%d, mode=%s", path, fileInfo.Size(), fileInfo.Mode().String())
		}
	}

	// Try to open the file with a few retries for better reliability
	var f *os.File
	var openErr error
	for i := 0; i < 3; i++ {
		f, openErr = os.Open(path)
		if openErr == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if openErr != nil {
		// More descriptive error for debugging
		fileType := "HLS segment"
		if strings.HasSuffix(file, ".m3u8") {
			fileType = "HLS playlist"
		}
		errMsg := fmt.Sprintf("%s not available: %v", fileType, openErr)
		if m.relayManager != nil && m.relayManager.Logger != nil {
			m.relayManager.Logger.Error("HLS file access error: %s", errMsg)
		}
		http.Error(w, errMsg, http.StatusNotFound)
		return
	}
	defer f.Close()

	if strings.HasSuffix(file, ".m3u8") {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	} else if strings.HasSuffix(file, ".ts") {
		w.Header().Set("Content-Type", "video/MP2T")
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	if m.relayManager != nil && m.relayManager.Logger != nil {
		m.relayManager.Logger.Debug("Serving file: %s", path)
	}
	io.Copy(w, f)
}

// Enhanced cleanup with viewer heartbeat checking
func (m *HLSManager) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(m.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if m.relayManager != nil && m.relayManager.Logger != nil {
				m.relayManager.Logger.Info("HLSManager cleanupLoop exiting due to shutdown")
			}
			return
		case <-ticker.C:
			now := time.Now()
			m.mu.Lock()
			for name, sess := range m.sessions {
				// Clean up stale viewers (no heartbeat for 30 seconds)
				for viewerID, lastHeartbeat := range sess.ViewerIDs {
					if now.Sub(lastHeartbeat) > 30*time.Second {
						delete(sess.ViewerIDs, viewerID)
						if m.relayManager != nil && m.relayManager.Logger != nil {
							m.relayManager.Logger.Info("Removed stale viewer %s from inputName=%s", viewerID, name)
						}
					}
				}
				shouldCleanup := false
				if len(sess.ViewerIDs) == 0 {
					shouldCleanup = now.Sub(sess.LastAccess) > m.sessionTimeout
				} else {
					shouldCleanup = now.Sub(sess.LastAccess) > (m.sessionTimeout * 3)
				}
				if shouldCleanup {
					if sess.IsConsumer && m.relayManager != nil {
						m.relayManager.StopInputRelayForConsumer(sess.InputName)
					}
					sess.Proc.Stop(2 * time.Second)
					os.RemoveAll(sess.Dir)
					delete(m.sessions, name)
					if m.relayManager != nil && m.relayManager.Logger != nil {
						m.relayManager.Logger.Info("Cleaned up HLS session for inputName=%s", name)
					}
				}
			}
			m.mu.Unlock()
		}
	}
}

// WriteEndlistToAll writes a final playlist with #EXT-X-ENDLIST for all active HLS sessions.
func (m *HLSManager) WriteEndlistToAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, sess := range m.sessions {
		playlistPath := filepath.Join(sess.Dir, "index.m3u8")
		// Read the current playlist (if exists)
		var lines []string
		if data, err := os.ReadFile(playlistPath); err == nil {
			lines = strings.Split(string(data), "\n")
			// Remove any existing #EXT-X-ENDLIST
			var filtered []string
			for _, l := range lines {
				if !strings.HasPrefix(l, "#EXT-X-ENDLIST") {
					filtered = append(filtered, l)
				}
			}
			lines = filtered
		}
		// Append #EXT-X-ENDLIST
		lines = append(lines, "#EXT-X-ENDLIST")
		final := strings.Join(lines, "\n")
		if err := os.WriteFile(playlistPath, []byte(final), 0644); err == nil {
			if m.relayManager != nil && m.relayManager.Logger != nil {
				m.relayManager.Logger.Info("Wrote #EXT-X-ENDLIST to playlist for inputName=%s", name)
			}
		}
	}
}
