package stream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-mls/internal/logger"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sync/singleflight"
)

// RelayManagerAPI is replaced by StreamProvider
// type RelayManagerAPI interface { ... }

// MapViewerManager is the default implementation using in-memory maps (concurrent-safe via HLSManager.mu).
type MapViewerManager struct {
	sess *HLSSession
}

func (v *MapViewerManager) AddViewer() (string, error) {
	v.sess.Mu.Lock()
	defer v.sess.Mu.Unlock()
	viewerID := generateViewerID()
	v.sess.ViewerIDs[viewerID] = time.Now()
	v.sess.LastAccess = time.Now()
	return viewerID, nil
}

func (v *MapViewerManager) UpdateViewerHeartbeat(viewerID string) error {
	v.sess.Mu.Lock()
	defer v.sess.Mu.Unlock()
	if _, exists := v.sess.ViewerIDs[viewerID]; !exists {
		return errors.New("viewerID not found")
	}
	v.sess.ViewerIDs[viewerID] = time.Now()
	v.sess.LastAccess = time.Now()
	return nil
}

func (v *MapViewerManager) RemoveViewer(viewerID string) error {
	v.sess.Mu.Lock()
	defer v.sess.Mu.Unlock()
	delete(v.sess.ViewerIDs, viewerID)
	return nil
}

func generateViewerID() string {
	return "viewer-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

type HLSSession struct {
	// Immutable fields (set at creation, never change)
	InputName  string
	LocalURL   string
	InputURL   string // The original input URL (e.g. file://... or rtsp://...)
	Dir        string
	IsConsumer bool // Whether this session is registered as an input relay consumer

	// --- Concurrency: mutable fields below are protected by Mu ---
	ViewerIDs  map[string]time.Time // Track individual viewers with heartbeat
	LastAccess time.Time            // Last time any viewer accessed this session
	Ready      bool                 // Session readiness flag
	Mu         sync.RWMutex         // Protects all mutable fields above

	// --- Process management (concurrent-safe via FFmpegProcess interface) ---
	Proc FFmpegProcess // FFmpeg process abstraction (handles concurrency and output capture)

	ViewerManager *MapViewerManager // Per-session viewer management
}

type HLSManager struct {
	// --- Mutable fields protected by mu ---
	sessions     map[string]*HLSSession
	failedInputs map[string]time.Time // Track failed input attempts for cooldown
	requestGroup singleflight.Group   // Synchronize session creation

	// --- Immutable/config fields (set at construction) ---
	Logger         *logger.Logger
	streamProvider StreamProvider // Replaces RelayManagerAPI
	streamManager  *StreamManager // For consumer registration
	cleanupTicker  *time.Ticker
	cleanupDone    chan struct{}
	ctx            context.Context
	cancel         context.CancelFunc
	config         HLSManagerConfig // Store full config for access to tuning params
	mu             sync.RWMutex     // Protects sessions and failedInputs
}

// HLSManagerConfig holds all configuration for HLSManager using time.Duration fields only
// This is used to decouple config.Duration from the rest of the codebase
// and keep all business logic using time.Duration
type HLSManagerConfig struct {
	CleanupInterval        time.Duration
	SessionTimeout         time.Duration
	FailedCooldown         time.Duration
	PlaylistReadyTimeout   time.Duration
	PlaylistPollInterval   time.Duration
	PlaylistPollAttempts   int
	ViewerHeartbeatTimeout time.Duration
	FFmpegStopTimeout      time.Duration
	PlaylistBaseDir        string
	SegmentDuration        time.Duration
	PlaylistSize           int
	FFmpegPreset           string
}

// NewHLSManager creates a new HLS manager
func NewHLSManager(l *logger.Logger, cfg HLSManagerConfig, streamProvider StreamProvider, streamManager *StreamManager) *HLSManager {
	// Ensure HLS directory exists
	if err := os.MkdirAll(cfg.PlaylistBaseDir, 0755); err != nil {
		l.Error("Failed to create HLS directory", "dir", cfg.PlaylistBaseDir, "err", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	hm := &HLSManager{
		sessions:       make(map[string]*HLSSession),
		failedInputs:   make(map[string]time.Time),
		Logger:         l,
		streamProvider: streamProvider,
		streamManager:  streamManager,
		cleanupTicker:  time.NewTicker(cfg.CleanupInterval),
		cleanupDone:    make(chan struct{}),
		ctx:            ctx,
		cancel:         cancel,
		config:         cfg,
	}

	// Start cleanup loop
	go hm.cleanupLoop(ctx)

	return hm
}

// SetViewerManager allows injection of a custom ViewerManager (for testing or extension).
func (m *HLSManager) SetViewerManager(vmFactory func(sess *HLSSession) *MapViewerManager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, sess := range m.sessions {
		sess.ViewerManager = vmFactory(sess)
	}
}

// Start or get an HLS session for the given input
// GetOrStartSession starts or retrieves an HLS session for the given input.
func (m *HLSManager) GetOrStartSession(inputName, localURL string) (*HLSSession, error) {
	m.mu.Lock()
	// Check for recent failure
	if err := m.checkFailedCooldown(inputName); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	// Validate inputName (no path traversal)
	if err := m.validateInputName(inputName); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	sess, exists := m.sessions[inputName]
	if exists {
		sess.LastAccess = time.Now()
		m.mu.Unlock()
		return sess, nil
	}
	m.mu.Unlock()

	// Use singleflight to ensure only one goroutine creates the session for this input
	v, err, _ := m.requestGroup.Do(inputName, func() (interface{}, error) {
		// Double-check if session was created while we were waiting for the lock/singleflight
		m.mu.Lock()
		if sess, exists := m.sessions[inputName]; exists {
			sess.LastAccess = time.Now()
			m.mu.Unlock()
			return sess, nil
		}
		m.mu.Unlock()

		// Start input relay as a consumer if stream provider is available
		actualLocalURL, err := m.streamProvider.GetStream(inputName)
		if err != nil {
			return nil, err
		}
		dir, err := m.createHLSTempDir(inputName)
		if err != nil {
			m.streamProvider.ReleaseStream(inputName)
			return nil, err
		}
		proc, err := m.createAndStartFFmpegProcess(actualLocalURL, dir)
		if err != nil {
			os.RemoveAll(dir)
			m.streamProvider.ReleaseStream(inputName)
			return nil, err
		}

		sess := &HLSSession{
			InputName:  inputName,
			LocalURL:   actualLocalURL,
			InputURL:   "", // Will be populated if possible
			Dir:        dir,
			IsConsumer: m.streamProvider != nil,
			ViewerIDs:  make(map[string]time.Time),
			LastAccess: time.Now(),
			Proc:       proc,
			Ready:      false,
		}

		// Try to resolve InputURL from StreamProvider if it's an InputRelayManager
		if irm, ok := m.streamProvider.(*InputRelayManager); ok {
			if url, exists := irm.GetInputURLByName(inputName); exists {
				sess.InputURL = url
			}
		}
		sess.ViewerManager = &MapViewerManager{sess: sess}

		m.mu.Lock()
		m.sessions[inputName] = sess
		m.mu.Unlock()
		m.Logger.Info("Created new HLS session", "inputName", inputName)

		// Register as consumer in StreamManager's consumer registry
		if m.streamManager != nil && sess.InputURL != "" {
			consumer := NewHLSConsumer(m, inputName, sess.InputURL)
			m.streamManager.consumerRegistry.Register(sess.InputURL, consumer)
			m.Logger.Debug("Registered HLS consumer", "inputURL", sess.InputURL, "inputName", inputName, "consumerID", consumer.GetConsumerID())
		} else if m.streamManager != nil {
			m.Logger.Warn("Could not register HLS consumer: InputURL not found", "inputName", inputName)
		}

		// Start playlist readiness monitoring in a separate goroutine
		go m.monitorPlaylistReadiness(sess, inputName)

		return sess, nil
	})

	if err != nil {
		return nil, err
	}

	return v.(*HLSSession), nil
}

// --- Refactored helpers for GetOrStartSession ---

// checkFailedCooldown checks if the input is in failed cooldown.
func (m *HLSManager) checkFailedCooldown(inputName string) error {
	if failTime, failed := m.failedInputs[inputName]; failed {
		if time.Since(failTime) < m.config.FailedCooldown {
			m.Logger.Warn("Input in failed cooldown, refusing to start session", "inputName", inputName)
			return errors.New("input unavailable (cooldown)")
		}
		// Cooldown expired, remove
		delete(m.failedInputs, inputName)
	}
	return nil
}

// validateInputName checks for path traversal or invalid characters.
func (m *HLSManager) validateInputName(inputName string) error {
	if strings.Contains(inputName, "..") || strings.ContainsAny(inputName, "/\\") {
		m.Logger.Error("Invalid input name", "inputName", inputName)
		return errors.New("invalid input name")
	}
	return nil
}

// startInputRelayIfNeeded starts the input relay if not already running
// Returns the local RTSP URL and true if started/running, or error
func (hm *HLSManager) startInputRelayIfNeeded(inputName string) (string, bool, error) {
	if hm.streamProvider == nil {
		return "", false, fmt.Errorf("stream provider not initialized")
	}

	localURL, err := hm.streamProvider.GetStream(inputName)
	if err != nil {
		return "", false, err
	}

	return localURL, true, nil
}

// stopInputRelayIfNeeded stops the input relay if it was started by this session
func (hm *HLSManager) stopInputRelayIfNeeded(sess *HLSSession) {
	if sess.IsConsumer && hm.streamProvider != nil {
		hm.Logger.Info("Stopping input relay consumer for HLS session", "inputName", sess.InputName)
		hm.streamProvider.ReleaseStream(sess.InputName)
		sess.IsConsumer = false
	}
}

// createHLSTempDir creates a temporary directory for HLS segments.
func (m *HLSManager) createHLSTempDir(inputName string) (string, error) {
	dir, err := os.MkdirTemp(m.config.PlaylistBaseDir, "hls_"+inputName+"_")
	if err != nil {
		m.Logger.Error("Failed to create temp dir", "inputName", inputName, "err", err)
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	m.Logger.Info("Created HLS temp dir", "inputName", inputName, "dir", dir)
	return dir, nil
}

// createAndStartFFmpegProcess creates and starts the ffmpeg process for HLS.
func (m *HLSManager) createAndStartFFmpegProcess(localURL, dir string) (FFmpegProcess, error) {
	playlist := filepath.Join(dir, "index.m3u8")
	segmentPattern := filepath.Join(dir, "segment_%03d.ts")
	ffmpegArgs := []string{
		"-rtsp_transport", "tcp",
		"-analyzeduration", "500k",
		"-probesize", "500k",
		"-fflags", "nobuffer",
		"-i", localURL,
		"-c:v", "libx264",
		"-preset", m.config.FFmpegPreset,
		"-tune", "zerolatency",
		"-c:a", "aac",
		"-ac", "2",
		"-ar", "44100",
		"-f", "hls",
		"-hls_time", strconv.FormatFloat(m.config.SegmentDuration.Seconds(), 'f', -1, 64),
		"-hls_list_size", strconv.Itoa(m.config.PlaylistSize),
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
		return nil, fmt.Errorf("failed to create ffmpeg process: %w", err)
	}
	if err := proc.Start(procCtx); err != nil {
		return nil, fmt.Errorf("failed to start ffmpeg: %w", err)
	}
	procCancel = nil // Ownership transferred to process
	return proc, nil
}

// monitorPlaylistReadiness monitors the playlist file and sets the session Ready flag.
func (m *HLSManager) monitorPlaylistReadiness(sess *HLSSession, inputName string) {
	playlistPath := filepath.Join(sess.Dir, "index.m3u8")
	ready := m.tryFsnotifyPlaylistReady(playlistPath)
	if !ready {
		m.Logger.Warn("Falling back to polling for playlist readiness", "playlistPath", playlistPath)
		ready = m.tryPollPlaylistReady(playlistPath)
	}
	m.setSessionReadiness(sess, inputName, ready)
}

// tryFsnotifyPlaylistReady tries to detect playlist readiness using fsnotify.
func (m *HLSManager) tryFsnotifyPlaylistReady(playlistPath string) bool {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return false
	}
	defer watcher.Close()
	_ = watcher.Add(filepath.Dir(playlistPath))
	playlistReadyTimeout := m.getPlaylistReadyTimeout()
	timeout := time.After(playlistReadyTimeout)
	for {
		if fi, err := os.Stat(playlistPath); err == nil && fi.Size() > 0 {
			m.Logger.Debug("[fsnotify] Stat playlist", "path", playlistPath, "size", fi.Size(), "mod", fi.ModTime())
			return true
		}
		select {
		case event := <-watcher.Events:
			m.Logger.Debug("[fsnotify] Event", "event", event)
			if event.Name == playlistPath && (event.Op&fsnotify.Create != 0 || event.Op&fsnotify.Write != 0) {
				if fi, err := os.Stat(playlistPath); err == nil && fi.Size() > 0 {
					m.Logger.Debug("[fsnotify] Write/Create", "path", playlistPath, "size", fi.Size(), "mod", fi.ModTime())
					return true
				}
			}
		case <-timeout:
			return false
		case <-time.After(10 * time.Second):
			// continue
		}
	}
}

// tryPollPlaylistReady polls for playlist readiness.
func (m *HLSManager) tryPollPlaylistReady(playlistPath string) bool {
	pollAttempts := 60 // 60 attempts × 500ms = 30 seconds timeout
	for i := 0; i < pollAttempts; i++ {
		fileInfo, err := os.Stat(playlistPath)
		if err == nil {
			m.Logger.Debug("[poll] Attempt", "attempt", i+1, "path", playlistPath, "size", fileInfo.Size(), "mod", fileInfo.ModTime())
			if fileInfo.Size() > 0 {
				return true
			}
		} else {
			m.Logger.Debug("[poll] Stat error", "attempt", i+1, "err", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// setSessionReadiness sets the session readiness and logs output/errors.
func (m *HLSManager) setSessionReadiness(sess *HLSSession, inputName string, ready bool) {
	sess.Mu.Lock()
	sess.Ready = ready
	sess.Mu.Unlock()
	if ready {
		m.Logger.Info("HLS session ready", "inputName", inputName)
		return
	}
	m.Logger.Error("HLS session failed to become ready", "inputName", inputName)
	if sess.Proc != nil {
		output := sess.Proc.GetOutput()
		lines := strings.Split(strings.TrimSpace(output), "\n")
		if len(lines) > 40 {
			lines = lines[len(lines)-40:]
		}
		for _, line := range lines {
			if line != "" {
				m.Logger.Error("ffmpeg output", "line", line)
			}
		}
	}
}

// AddViewer adds a new viewer to the session and returns a viewer ID
func (m *HLSManager) AddViewer(inputName string) (string, error) {
	m.mu.Lock()
	sess, ok := m.sessions[inputName]
	m.mu.Unlock()
	if !ok || sess == nil {
		// Try to create the session on-demand
		var err error
		sess, err = m.GetOrStartSession(inputName, "")
		if err != nil {
			m.Logger.Error("AddViewer: failed to create session", "inputName", inputName, "err", err)
			return "", errors.New("failed to create session: " + err.Error())
		}
		m.Logger.Info("AddViewer: created session on-demand", "inputName", inputName)
	}
	if sess.ViewerManager == nil {
		return "", errors.New("session ViewerManager not set")
	}
	viewerID, err := sess.ViewerManager.AddViewer()
	if err != nil {
		m.Logger.Error("AddViewer: failed to add viewer", "inputName", inputName, "err", err)
		return "", err
	}
	m.Logger.Info("AddViewer: added viewer", "viewerID", viewerID, "inputName", inputName)
	return viewerID, nil
}

// UpdateViewerHeartbeat updates the heartbeat for a viewer
func (m *HLSManager) UpdateViewerHeartbeat(inputName, viewerID string) error {
	m.mu.Lock()
	sess, ok := m.sessions[inputName]
	m.mu.Unlock()
	if !ok || sess == nil {
		return errors.New("session not found")
	}
	if sess.ViewerManager == nil {
		return errors.New("session ViewerManager not set")
	}
	return sess.ViewerManager.UpdateViewerHeartbeat(viewerID)
}

// RemoveViewer removes a viewer from the session
func (m *HLSManager) RemoveViewer(inputName, viewerID string) error {
	m.mu.Lock()
	sess, ok := m.sessions[inputName]
	m.mu.Unlock()
	if !ok || sess == nil {
		return errors.New("session not found")
	}
	if sess.ViewerManager == nil {
		return errors.New("session ViewerManager not set")
	}
	return sess.ViewerManager.RemoveViewer(viewerID)
}

// Shutdown gracefully stops the cleanup loop
func (m *HLSManager) Shutdown() {
	m.cancel()
	m.mu.Lock()
	names := make([]string, 0, len(m.sessions))
	for name := range m.sessions {
		names = append(names, name)
	}
	m.mu.Unlock()
	for _, name := range names {
		m.DeleteSession(name)
	}
}

// ServeHLS serves HLS playlist or segment, concurrency-safe and with detailed logging
func (m *HLSManager) ServeHLS(w http.ResponseWriter, r *http.Request, inputName, file string, localURL string) {
	m.Logger.Debug("ServeHLS request", "inputName", inputName, "file", file)

	// --- Stale viewer check ---
	if handled := m.serveHLSCheckViewer(w, r, inputName); handled {
		return
	}

	sess, handled := m.serveHLSGetSession(w, inputName, file)
	if handled {
		return
	}

	if handled := m.serveHLSWaitForReady(w, r, sess, inputName); handled {
		return
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

	if handled := m.serveHLSCheckPlaylist(w, path, file); handled {
		return
	}

	f, openErr := m.serveHLSOpenFile(path, file)
	if openErr != nil {
		m.Logger.Error("HLS file access error", "err", openErr)
		http.Error(w, openErr.Error(), http.StatusNotFound)
		return
	}
	defer f.Close()

	m.serveHLSWriteHeaders(w, file)
	m.Logger.Debug("Serving file", "path", path)
	io.Copy(w, f)
}

// serveHLSCheckViewer handles viewerID logic and returns true if the request is handled.
func (m *HLSManager) serveHLSCheckViewer(w http.ResponseWriter, r *http.Request, inputName string) bool {
	viewerID := r.URL.Query().Get("viewerID")
	if viewerID == "" {
		return false
	}
	m.mu.Lock()
	sess, exists := m.sessions[inputName]
	if !exists {
		m.mu.Unlock()
		m.Logger.Warn("ServeHLS: inputName not found for viewerID", "inputName", inputName, "viewerID", viewerID)
		http.Error(w, "Viewer session expired or invalid", http.StatusGone) // Use 410 for missing session
		return true
	}
	if sess.ViewerManager == nil {
		m.mu.Unlock()
		m.Logger.Warn("ServeHLS: ViewerManager missing for inputName viewerID", "inputName", inputName, "viewerID", viewerID)
		http.Error(w, "Viewer session expired or invalid", http.StatusGone) // Use 410 for missing ViewerManager
		return true
	}
	last, ok := sess.ViewerIDs[viewerID]
	if !ok || time.Since(last) > m.getViewerHeartbeatTimeout() {
		delete(sess.ViewerIDs, viewerID)
		m.Logger.Warn("Stale or missing viewerID; denying request", "viewerID", viewerID, "inputName", inputName)
		m.mu.Unlock()
		http.Error(w, "Viewer session expired or invalid", http.StatusGone)
		return true
	}
	sess.ViewerIDs[viewerID] = time.Now()
	sess.LastAccess = time.Now()
	m.mu.Unlock()
	return false
}

// serveHLSGetSession fetches the session and returns (session, handled).
func (m *HLSManager) serveHLSGetSession(w http.ResponseWriter, inputName, file string) (*HLSSession, bool) {
	m.mu.Lock()
	sess, exists := m.sessions[inputName]
	m.mu.Unlock()
	if !exists {

		m.Logger.WarnRateLimited("HLSManager: input not found for file", "inputName", inputName, "file", file)
		http.Error(w, "HLS session not found", http.StatusNotFound)
		return nil, true
	}
	return sess, false
}

// serveHLSWaitForReady waits for session readiness, returns true if handled.
func (m *HLSManager) serveHLSWaitForReady(w http.ResponseWriter, r *http.Request, sess *HLSSession, inputName string) bool {
	ready := func() bool {
		sess.Mu.RLock()
		defer sess.Mu.RUnlock()
		return sess.Ready
	}
	waitCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	for !ready() {
		select {
		case <-waitCtx.Done():
			m.Logger.Error("HLS session not ready for inputName", "inputName", inputName)
			http.Error(w, "HLS session not ready yet, please try again", http.StatusServiceUnavailable)
			return true
		default:
			time.Sleep(200 * time.Millisecond)
		}
	}
	return false
}

// serveHLSCheckPlaylist checks playlist existence and readiness.
func (m *HLSManager) serveHLSCheckPlaylist(w http.ResponseWriter, path, file string) bool {
	if strings.HasSuffix(file, ".m3u8") {
		fileInfo, statErr := os.Stat(path)
		if statErr != nil {
			m.Logger.Error("HLS playlist not available", "err", statErr)
			http.Error(w, "HLS playlist not available: "+statErr.Error(), http.StatusNotFound)
			return true
		}
		if fileInfo.Size() == 0 {
			// If the file exists but is empty, wait a bit for it to be populated
			time.Sleep(500 * time.Millisecond)
		}
		m.Logger.Debug("HLS playlist request", "path", path, "size", fileInfo.Size(), "mode", fileInfo.Mode().String())
	}
	return false
}

// serveHLSOpenFile tries to open the file with retries, returns file and error.
func (m *HLSManager) serveHLSOpenFile(path, file string) (*os.File, error) {
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
		fileType := "HLS segment"
		if strings.HasSuffix(file, ".m3u8") {
			fileType = "HLS playlist"
		}
		return nil, fmt.Errorf("%s not available: %v", fileType, openErr)
	}
	return f, nil
}

// serveHLSWriteHeaders sets appropriate headers for playlist/segment.
func (m *HLSManager) serveHLSWriteHeaders(w http.ResponseWriter, file string) {
	if strings.HasSuffix(file, ".m3u8") {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	} else if strings.HasSuffix(file, ".ts") {
		w.Header().Set("Content-Type", "video/MP2T")
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
}

// DeleteSession immediately stops and removes the HLS session for the given inputName, and deletes dir immediately.
func (m *HLSManager) DeleteSession(inputName string) {
	m.mu.Lock()
	sess, exists := m.sessions[inputName]
	if !exists {
		m.mu.Unlock()
		m.Logger.Warn("HLSManager: DeleteSession called for non-existent inputName", "inputName", inputName)
		return
	}

	// Unregister consumer from registry
	if m.streamManager != nil && sess.InputURL != "" {
		unregistered := m.streamManager.UnregisterConsumer(sess.InputURL, "hls-"+inputName)
		if unregistered {
			m.Logger.Debug("Unregistered HLS consumer", "inputURL", sess.InputURL, "inputName", inputName)
		}
	}

	if sess.Proc != nil {
		sess.Proc.Stop(m.ctx, m.getFFmpegStopTimeout())
		sess.Proc.Wait() // Ensure process is fully cleaned up
	}
	// Remove session from map immediately so new viewers can't join
	delete(m.sessions, inputName)
	m.mu.Unlock()
	os.RemoveAll(sess.Dir)
	m.Logger.Info("Deleted HLS session and directory immediately", "inputName", inputName)
}

// Enhanced cleanup with viewer heartbeat checking
func (m *HLSManager) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(m.config.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.Logger.Info("HLSManager cleanupLoop exiting due to shutdown")
			return
		case <-ticker.C:
			now := time.Now()
			m.mu.Lock()
			var sessionsToDelete []string
			for name, sess := range m.sessions {
				// --- Remove stale viewers ---
				// Any viewer with no heartbeat for more than the configured timeout is removed.
				// Collect stale viewer IDs first to avoid concurrent map iteration and modification
				var staleViewers []string
				sess.Mu.RLock()
				for viewerID, lastHeartbeat := range sess.ViewerIDs {
					if now.Sub(lastHeartbeat) > m.getViewerHeartbeatTimeout() {
						staleViewers = append(staleViewers, viewerID)
					}
				}
				lastAccess := sess.LastAccess
				sess.Mu.RUnlock()

				// Remove stale viewers using the proper API
				for _, viewerID := range staleViewers {
					if sess.ViewerManager != nil {
						if err := sess.ViewerManager.RemoveViewer(viewerID); err != nil {
							m.Logger.Warn("Failed to remove stale viewer", "viewerID", viewerID, "inputName", name, "err", err)
						} else {
							m.Logger.Info("Removed stale viewer", "viewerID", viewerID, "inputName", name)
						}
					}
				}

				// Get the current number of viewers after cleanup
				sess.Mu.RLock()
				numViewers := len(sess.ViewerIDs)
				sess.Mu.RUnlock()

				// --- Decide if session should be cleaned up ---
				// If no viewers, session is cleaned up after sessionTimeout.
				// If viewers remain, session is cleaned up after 3x sessionTimeout (zombie session protection).
				shouldCleanup := false
				if numViewers == 0 {
					shouldCleanup = now.Sub(lastAccess) > m.config.SessionTimeout
				} else {
					shouldCleanup = now.Sub(lastAccess) > (m.config.SessionTimeout * 3)
				}
				if shouldCleanup {
					sessionsToDelete = append(sessionsToDelete, name)
				}
			}
			m.mu.Unlock()
			for _, name := range sessionsToDelete {
				m.DeleteSession(name)
			}
		}
	}
}

// WriteEndlist writes #EXT-X-ENDLIST to the playlist for a single inputName.
func (m *HLSManager) WriteEndlist(inputName string) {
	m.mu.Lock()
	sess, exists := m.sessions[inputName]
	m.mu.Unlock()
	if !exists || sess == nil {
		m.Logger.Warn("WriteEndlist: session not found", "inputName", inputName)
		return
	}
	playlistPath := filepath.Join(sess.Dir, "index.m3u8")
	var lines []string
	if data, err := os.ReadFile(playlistPath); err == nil {
		lines = strings.Split(string(data), "\n")
		var filtered []string
		for _, l := range lines {
			if !strings.HasPrefix(l, "#EXT-X-ENDLIST") {
				filtered = append(filtered, l)
			}
		}
		lines = filtered
	}
	lines = append(lines, "#EXT-X-ENDLIST")
	final := strings.Join(lines, "\n")
	if err := os.WriteFile(playlistPath, []byte(final), 0644); err == nil {
		m.Logger.Info("Wrote #EXT-X-ENDLIST to playlist", "inputName", inputName)
	}
}

// Helper methods to get config-driven durations/intervals (with sane defaults)
func (m *HLSManager) getPlaylistReadyTimeout() time.Duration {
	return 10 * time.Second // Default timeout
}
func (m *HLSManager) getFFmpegStopTimeout() time.Duration {
	return 5 * time.Second // Default timeout
}

// Helper for proc stop timeout
func (m *HLSManager) getProcStopTimeout() time.Duration {
	// Return default timeout
	return 5 * time.Second // fallback default
}

// Helper for viewer heartbeat timeout (configurable, with fallback)
func (m *HLSManager) getViewerHeartbeatTimeout() time.Duration {
	return 30 * time.Second // Default hearbeat timeout
}
