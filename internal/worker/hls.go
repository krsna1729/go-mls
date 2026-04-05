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
	mu            sync.Mutex
	sessions      map[string]*hlsSession
	stopCh        chan struct{}
	wg            sync.WaitGroup
}

type hlsSession struct {
	proc        Process
	playlistDir string
	viewers     map[string]time.Time
}

func NewHLSManager(store *state.Store, log *logger.Logger, baseDir, preset string, rtmpPort int, viewerTimeout time.Duration) *HLSManager {
	if viewerTimeout <= 0 {
		viewerTimeout = 30 * time.Second
	}

	m := &HLSManager{
		store:         store,
		log:           log.With("component", "hls"),
		baseDir:       baseDir,
		preset:        preset,
		rtmpPort:      rtmpPort,
		viewerTimeout: viewerTimeout,
		sessions:      make(map[string]*hlsSession),
		stopCh:        make(chan struct{}),
	}
	m.wg.Add(1)
	go m.cleanupLoop()
	return m
}

func (m *HLSManager) AddViewer(ctx context.Context, streamPath string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupExpiredLocked(time.Now())

	viewerID, err := newViewerID()
	if err != nil {
		return "", fmt.Errorf("generate viewer id: %w", err)
	}

	sess, exists := m.sessions[streamPath]
	if exists {
		sess.viewers[viewerID] = time.Now()
		viewerCount := len(sess.viewers)
		m.log.Info("HLS viewer added", "stream_path", streamPath, "viewer_id", viewerID, "viewers", viewerCount)
		m.store.UpdateHLSViewerCount(streamPath, viewerCount)
		return viewerID, nil
	}

	playlistDir := filepath.Join(m.baseDir, streamPath)
	if err := os.MkdirAll(playlistDir, 0755); err != nil {
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

	m.log.Info("HLS generation started", "stream_path", streamPath, "viewer_id", viewerID)
	return viewerID, nil
}

func (m *HLSManager) Heartbeat(streamPath, viewerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupExpiredLocked(time.Now())

	sess, exists := m.sessions[streamPath]
	if !exists {
		return fmt.Errorf("session not found")
	}
	if _, ok := sess.viewers[viewerID]; !ok {
		return fmt.Errorf("session not found")
	}
	sess.viewers[viewerID] = time.Now()
	return nil
}

func (m *HLSManager) RemoveViewer(streamPath, viewerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, exists := m.sessions[streamPath]
	if !exists {
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
	m.stopSessionIfUnusedLocked(streamPath, sess)
}

func (m *HLSManager) Shutdown() {
	close(m.stopCh)
	m.wg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	for path, sess := range m.sessions {
		sess.proc.Stop()
		os.RemoveAll(sess.playlistDir)
		delete(m.sessions, path)
	}
	m.log.Info("HLSManager shutdown complete")
}

func (m *HLSManager) cleanupLoop() {
	defer m.wg.Done()

	interval := m.viewerTimeout / 2
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.mu.Lock()
			m.cleanupExpiredLocked(time.Now())
			m.mu.Unlock()
		case <-m.stopCh:
			return
		}
	}
}

func (m *HLSManager) cleanupExpiredLocked(now time.Time) {
	for streamPath, sess := range m.sessions {
		for viewerID, lastSeen := range sess.viewers {
			if now.Sub(lastSeen) > m.viewerTimeout {
				delete(sess.viewers, viewerID)
				m.log.Info("HLS viewer expired", "stream_path", streamPath, "viewer_id", viewerID)
			}
		}
		m.stopSessionIfUnusedLocked(streamPath, sess)
	}
}

func (m *HLSManager) stopSessionIfUnusedLocked(streamPath string, sess *hlsSession) {
	viewerCount := len(sess.viewers)
	if viewerCount > 0 {
		m.store.UpdateHLSViewerCount(streamPath, viewerCount)
		return
	}

	sess.proc.Stop()
	delete(m.sessions, streamPath)
	m.store.RemoveHLSSession(streamPath)
	os.RemoveAll(sess.playlistDir)
	m.log.Info("HLS generation stopped (no viewers)", "stream_path", streamPath)
}

func newViewerID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
