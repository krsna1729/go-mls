package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"go-mls/internal/logger"
	"go-mls/internal/state"
)

type HLSManager struct {
	store    *state.Store
	log      *logger.Logger
	baseDir  string
	preset   string
	rtmpPort int
	mu       sync.Mutex
	sessions map[string]*hlsSession
}

type hlsSession struct {
	proc        *FFmpegProcess
	viewerCount int
	playlistDir string
}

func NewHLSManager(store *state.Store, log *logger.Logger, baseDir, preset string, rtmpPort int) *HLSManager {
	return &HLSManager{
		store:    store,
		log:      log.With("component", "hls"),
		baseDir:  baseDir,
		preset:   preset,
		rtmpPort: rtmpPort,
		sessions: make(map[string]*hlsSession),
	}
}

func (m *HLSManager) AddViewer(ctx context.Context, streamPath string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, exists := m.sessions[streamPath]
	if exists {
		sess.viewerCount++
		m.log.Info("HLS viewer added", "stream_path", streamPath, "viewers", sess.viewerCount)
		m.store.UpdateHLSViewerCount(streamPath, sess.viewerCount)
		return filepath.Join(sess.playlistDir, "index.m3u8"), nil
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

	fp, err := RunAndMonitorFFmpeg(ctx, m.store, m.log, args...)
	if err != nil {
		return "", fmt.Errorf("start HLS generator: %w", err)
	}

	sess = &hlsSession{
		proc:        fp,
		viewerCount: 1,
		playlistDir: playlistDir,
	}
	m.sessions[streamPath] = sess

	m.store.AddHLSSession(&state.HLSSession{
		StreamPath:  streamPath,
		PlaylistDir: playlistDir,
		ViewerCount: 1,
		PID:         fp.PID(),
	})

	m.log.Info("HLS generation started", "stream_path", streamPath)
	return playlistPath, nil
}

func (m *HLSManager) RemoveViewer(streamPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, exists := m.sessions[streamPath]
	if !exists {
		return
	}

	sess.viewerCount--
	m.log.Info("HLS viewer removed", "stream_path", streamPath, "viewers", sess.viewerCount)
	m.store.UpdateHLSViewerCount(streamPath, sess.viewerCount)

	if sess.viewerCount <= 0 {
		sess.proc.Stop()
		delete(m.sessions, streamPath)
		m.store.RemoveHLSSession(streamPath)
		os.RemoveAll(sess.playlistDir)
		m.log.Info("HLS generation stopped (no viewers)", "stream_path", streamPath)
	}
}

func (m *HLSManager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for path, sess := range m.sessions {
		sess.proc.Stop()
		os.RemoveAll(sess.playlistDir)
		delete(m.sessions, path)
	}
	m.log.Info("HLSManager shutdown complete")
}
