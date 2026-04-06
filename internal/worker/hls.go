package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go-mls/internal/ffmpeg"
	"go-mls/internal/logger"
	"go-mls/internal/state"
)

type HLSManager struct {
	store         *state.Store
	log           *logger.Logger
	baseDir       string
	preset        string
	rtmpPort      int
	viewerTimeout time.Duration
	idleTimeout   time.Duration
	mu            sync.Mutex
	sessions      map[string]*hlsSession
	stopCh        chan struct{}
	wg            sync.WaitGroup
}

type hlsSession struct {
	proc        Process
	playlistDir string
	viewers     map[string]time.Time
	idleSince   time.Time
}

type hlsCleanupTask struct {
	streamPath  string
	playlistDir string
	idleFor     time.Duration
}

func NewHLSManager(store *state.Store, log *logger.Logger, baseDir, preset string, rtmpPort int, viewerTimeout, idleTimeout time.Duration) *HLSManager {
	if viewerTimeout <= 0 {
		viewerTimeout = 30 * time.Second
	}
	if idleTimeout <= 0 {
		idleTimeout = 30 * time.Second
	}

	m := &HLSManager{
		store:         store,
		log:           log.With("component", "hls"),
		baseDir:       baseDir,
		preset:        preset,
		rtmpPort:      rtmpPort,
		viewerTimeout: viewerTimeout,
		idleTimeout:   idleTimeout,
		sessions:      make(map[string]*hlsSession),
		stopCh:        make(chan struct{}),
	}
	m.wg.Add(1)
	go m.cleanupLoop()
	return m
}

func (m *HLSManager) AddViewer(ctx context.Context, streamPath string) (string, error) {
	m.mu.Lock()
	cleanupTasks := m.cleanupExpiredLocked(time.Now())

	viewerID, err := newViewerID()
	if err != nil {
		m.mu.Unlock()
		m.runCleanupTasks(cleanupTasks)
		return "", fmt.Errorf("generate viewer id: %w", err)
	}

	sess, exists := m.sessions[streamPath]
	if exists {
		sess.viewers[viewerID] = time.Now()
		sess.idleSince = time.Time{}
		viewerCount := len(sess.viewers)
		m.log.Info("HLS viewer added", "stream_path", streamPath, "viewer_id", viewerID, "viewers", viewerCount)
		m.store.UpdateHLSViewerCount(streamPath, viewerCount)
		m.mu.Unlock()
		m.runCleanupTasks(cleanupTasks)
		return viewerID, nil
	}

	playlistDir := filepath.Join(m.baseDir, streamPath)
	if err := os.MkdirAll(playlistDir, 0755); err != nil {
		m.mu.Unlock()
		m.runCleanupTasks(cleanupTasks)
		return "", fmt.Errorf("create HLS dir: %w", err)
	}

	localInput := fmt.Sprintf("rtmp://127.0.0.1:%d/%s", m.rtmpPort, streamPath)
	playlistPath := filepath.Join(playlistDir, "index.m3u8")

	args := []string{
		"-i", localInput,
		"-c:v", "libx264", "-preset", m.preset,
		"-c:a", "aac",
		"-f", "hls",
		"-hls_time", "2",
		"-hls_list_size", "6",
		"-hls_flags", "delete_segments+append_list",
		playlistPath,
	}

	fp, err := ffmpeg.RunAndMonitor(ctx, m.store, m.log, args...)
	if err != nil {
		m.mu.Unlock()
		m.runCleanupTasks(cleanupTasks)
		return "", fmt.Errorf("start HLS generator: %w", err)
	}

	sess = &hlsSession{
		proc:        fp,
		playlistDir: playlistDir,
		viewers: map[string]time.Time{
			viewerID: time.Now(),
		},
	}
	m.sessions[streamPath] = sess

	m.store.AddHLSSession(&state.HLSSession{
		StreamPath:  streamPath,
		PlaylistDir: playlistDir,
		ViewerCount: 1,
		PID:         fp.PID(),
	})

	m.mu.Unlock()
	m.runCleanupTasks(cleanupTasks)
	m.log.Info("HLS generation started", "stream_path", streamPath, "viewer_id", viewerID)
	return viewerID, nil
}

func (m *HLSManager) Heartbeat(streamPath, viewerID string) error {
	m.mu.Lock()
	cleanupTasks := m.cleanupExpiredLocked(time.Now())

	sess, exists := m.sessions[streamPath]
	if !exists {
		m.mu.Unlock()
		m.runCleanupTasks(cleanupTasks)
		return fmt.Errorf("session not found")
	}
	if _, ok := sess.viewers[viewerID]; !ok {
		m.mu.Unlock()
		m.runCleanupTasks(cleanupTasks)
		return fmt.Errorf("session not found")
	}
	sess.viewers[viewerID] = time.Now()
	m.mu.Unlock()
	m.runCleanupTasks(cleanupTasks)
	return nil
}

func (m *HLSManager) RemoveViewer(streamPath, viewerID string) {
	m.mu.Lock()
	var cleanupTasks []hlsCleanupTask

	sess, exists := m.sessions[streamPath]
	if !exists {
		m.mu.Unlock()
		return
	}

	if viewerID == "" {
		for id := range sess.viewers {
			delete(sess.viewers, id)
			break
		}
	} else {
		delete(sess.viewers, viewerID)
	}

	m.log.Info("HLS viewer removed", "stream_path", streamPath, "viewer_id", viewerID, "viewers", len(sess.viewers))
	if task, ok := m.stopSessionIfUnusedLocked(streamPath, sess, time.Now()); ok {
		cleanupTasks = append(cleanupTasks, task)
	}
	m.mu.Unlock()
	m.runCleanupTasks(cleanupTasks)
}

func (m *HLSManager) Shutdown() {
	close(m.stopCh)
	m.wg.Wait()

	m.mu.Lock()

	cleanupDirs := make([]string, 0, len(m.sessions))

	for path, sess := range m.sessions {
		sess.proc.Stop()
		cleanupDirs = append(cleanupDirs, sess.playlistDir)
		delete(m.sessions, path)
	}
	m.mu.Unlock()

	for _, dir := range cleanupDirs {
		if err := os.RemoveAll(dir); err != nil {
			m.log.Warn("Failed to remove HLS playlist dir during shutdown", "playlist_dir", dir, "error", err)
		}
	}
	m.log.Info("HLSManager shutdown complete")
}

func (m *HLSManager) cleanupLoop() {
	defer m.wg.Done()

	interval := m.viewerTimeout / 2
	idleInterval := m.idleTimeout / 2
	if idleInterval < interval {
		interval = idleInterval
	}
	if interval < 2*time.Second {
		interval = 2 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.mu.Lock()
			cleanupTasks := m.cleanupExpiredLocked(time.Now())
			m.mu.Unlock()
			m.runCleanupTasks(cleanupTasks)
		case <-m.stopCh:
			return
		}
	}
}

func (m *HLSManager) cleanupExpiredLocked(now time.Time) []hlsCleanupTask {
	tasks := make([]hlsCleanupTask, 0)
	for streamPath, sess := range m.sessions {
		for viewerID, lastSeen := range sess.viewers {
			if now.Sub(lastSeen) > m.viewerTimeout {
				delete(sess.viewers, viewerID)
				m.log.Info("HLS viewer expired", "stream_path", streamPath, "viewer_id", viewerID)
			}
		}
		if task, ok := m.stopSessionIfUnusedLocked(streamPath, sess, now); ok {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

func (m *HLSManager) stopSessionIfUnusedLocked(streamPath string, sess *hlsSession, now time.Time) (hlsCleanupTask, bool) {
	viewerCount := len(sess.viewers)
	if viewerCount > 0 {
		sess.idleSince = time.Time{}
		m.store.UpdateHLSViewerCount(streamPath, viewerCount)
		return hlsCleanupTask{}, false
	}

	m.store.UpdateHLSViewerCount(streamPath, 0)
	if sess.idleSince.IsZero() {
		sess.idleSince = now
		m.log.Info("HLS session became idle", "stream_path", streamPath, "idle_timeout", m.idleTimeout.String())
		return hlsCleanupTask{}, false
	}

	if now.Sub(sess.idleSince) < m.idleTimeout {
		return hlsCleanupTask{}, false
	}

	idleFor := now.Sub(sess.idleSince)
	playlistDir := sess.playlistDir
	sess.proc.Stop()
	delete(m.sessions, streamPath)
	m.store.RemoveHLSSession(streamPath)
	m.log.Info("HLS generation stopped (idle timeout)", "stream_path", streamPath, "idle_for", idleFor.String())

	return hlsCleanupTask{streamPath: streamPath, playlistDir: playlistDir, idleFor: idleFor}, true
}

func (m *HLSManager) runCleanupTasks(tasks []hlsCleanupTask) {
	for _, task := range tasks {
		if err := os.RemoveAll(task.playlistDir); err != nil {
			m.log.Warn("Failed to remove HLS playlist dir", "stream_path", task.streamPath, "playlist_dir", task.playlistDir, "error", err)
		}
	}
}

func newViewerID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
