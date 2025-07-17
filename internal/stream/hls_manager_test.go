package stream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-mls/internal/logger"
)

// Error-injecting ViewerManager for tests
// Implements ViewerManager interface

type errorViewerManager struct {
	addViewerErr    error
	updateViewerErr error
	removeViewerErr error
}

func (e *errorViewerManager) AddViewer() (string, error) {
	return "testviewerid", e.addViewerErr
}
func (e *errorViewerManager) UpdateViewerHeartbeat(viewerID string) error {
	return e.updateViewerErr
}
func (e *errorViewerManager) RemoveViewer(viewerID string) error {
	return e.removeViewerErr
}

func TestServeHLS_PlaylistAndSegment(t *testing.T) {
	dir, err := os.MkdirTemp("", "hls_test_")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	playlistPath := filepath.Join(dir, "index.m3u8")
	segmentPath := filepath.Join(dir, "segment_001.ts")
	if err := os.WriteFile(playlistPath, []byte("#EXTM3U\n#EXT-X-VERSION:3\n"), 0644); err != nil {
		t.Fatalf("failed to write playlist: %v", err)
	}
	if err := os.WriteFile(segmentPath, []byte("dummytsdata"), 0644); err != nil {
		t.Fatalf("failed to write segment: %v", err)
	}

	mgr := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())
	inputName := "testinput"
	sess := &HLSSession{
		InputName: inputName,
		Dir:       dir,
		Ready:     true,
		ViewerIDs: make(map[string]time.Time),
		Proc:      &shutdownMockProc{}, // Ensure Proc is non-nil for cleanup safety
	}
	mgr.sessions[inputName] = sess

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file := strings.TrimPrefix(r.URL.Path, "/")
		mgr.ServeHLS(w, r, inputName, file, "")
	}))
	defer ts.Close()

	// Test playlist
	resp, err := http.Get(ts.URL + "/index.m3u8")
	if err != nil {
		t.Fatalf("GET playlist: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200 for playlist, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "#EXTM3U") {
		t.Errorf("playlist body missing expected content")
	}

	// Test segment
	resp, err = http.Get(ts.URL + "/segment_001.ts")
	if err != nil {
		t.Fatalf("GET segment: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200 for segment, got %d", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	if string(body) != "dummytsdata" {
		t.Errorf("segment body mismatch")
	}
}

func TestServeHLS_NotFoundRateLimit(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logr := logger.NewLoggerWithWriter(&buf)
	mgr := NewHLSManager(minimalHLSManagerConfig(), logr)
	inputName := "missinginput"
	file := "index.m3u8"

	// Call ServeHLS multiple times, expect logger to handle rate limiting
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/index.m3u8", nil)
		mgr.ServeHLS(w, r, inputName, file, "")
		if w.Result().StatusCode != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w.Result().StatusCode)
		}
	}
	// We do not check log count here; logger should handle rate limiting
}

func TestHLSManager_ConcurrentAPI(t *testing.T) {
	t.Parallel()
	logr := logger.NewLogger()
	dir := t.TempDir()
	mgr := NewHLSManager(minimalHLSManagerConfig(), logr)
	mgr.relayManager = NewRelayManager(logr, dir, "")

	num := 10
	var wg sync.WaitGroup
	inputNames := make([]string, num)
	for i := 0; i < num; i++ {
		inputNames[i] = "input" + string(rune('A'+i))
	}

	timeout := time.After(10 * time.Second)
	done := make(chan struct{})
	go func() {
		// GetOrStartSession concurrently
		for i := 0; i < num; i++ {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				_, _ = mgr.GetOrStartSession(name, "rtsp://localhost/relay/"+name)
			}(inputNames[i])
		}

		// ServeHLS concurrently (simulate playlist requests)
		for i := 0; i < num; i++ {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				w := httptest.NewRecorder()
				r := httptest.NewRequest("GET", "/index.m3u8", nil)
				mgr.ServeHLS(w, r, name, "index.m3u8", "rtsp://localhost/relay/"+name)
			}(inputNames[i])
		}

		// AddViewer, UpdateViewerHeartbeat, RemoveViewer concurrently
		for i := 0; i < num; i++ {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				viewerID, _ := mgr.AddViewer(name)
				mgr.UpdateViewerHeartbeat(name, viewerID)
				mgr.RemoveViewer(name, viewerID)
			}(inputNames[i])
		}

		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Test completed
	case <-timeout:
		t.Fatal("TestHLSManager_ConcurrentAPI timed out (possible deadlock or contention)")
	}
}

// --- Helper for minimal config ---
func minimalHLSManagerConfig() HLSManagerConfig {
	return HLSManagerConfig{
		CleanupInterval:        10 * time.Second,
		SessionTimeout:         30 * time.Second,
		FailedCooldown:         5 * time.Second,
		PlaylistReadyTimeout:   2 * time.Second,
		PlaylistPollInterval:   100 * time.Millisecond,
		PlaylistPollAttempts:   3,
		ViewerHeartbeatTimeout: 10 * time.Second,
		FFmpegStopTimeout:      2 * time.Second,
		PlaylistBaseDir:        os.TempDir(),
	}
}

func newTestLogger() *logger.Logger {
	return logger.NewLoggerWithWriter(io.Discard)
}

func TestNewHLSManager_CreatesManager(t *testing.T) {
	h := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())
	if h == nil {
		t.Fatal("expected non-nil HLSManager")
	}
	if h.sessions == nil {
		t.Error("expected sessions map to be initialized")
	}
}

func TestHLSManager_SetRelayManager(t *testing.T) {
	h := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())
	rm := &RelayManager{}
	h.SetRelayManager(rm)
	if h.relayManager != rm {
		t.Error("relayManager not set correctly")
	}
}

func TestHLSManager_GetOrStartSession_Basic(t *testing.T) {
	h := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())
	inputName := "testinput"
	localURL := "rtsp://localhost/relay/testinput"
	sess, err := h.GetOrStartSession(inputName, localURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess == nil {
		t.Fatal("expected non-nil session")
	}
	if sess.InputName != inputName {
		t.Errorf("InputName mismatch: got %q, want %q", sess.InputName, inputName)
	}
}

func TestHLSManager_AddViewer_Update_Remove(t *testing.T) {
	h := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())
	inputName := "testinput"
	_, _ = h.GetOrStartSession(inputName, "rtsp://localhost/relay/testinput")

	viewerID, err := h.AddViewer(inputName)
	if err != nil {
		t.Fatalf("AddViewer error: %v", err)
	}
	if viewerID == "" {
		t.Error("expected non-empty viewerID")
	}

	h.UpdateViewerHeartbeat(inputName, viewerID)
	h.RemoveViewer(inputName, viewerID)
	// No panic or error expected
}

func TestHLSManager_getPlaylistReadyTimeout(t *testing.T) {
	h := &HLSManager{config: HLSManagerConfig{PlaylistReadyTimeout: 123 * time.Second}}
	if got := h.getPlaylistReadyTimeout(); got != 123*time.Second {
		t.Errorf("expected 123s, got %v", got)
	}
	h.config.PlaylistReadyTimeout = 0
	if got := h.getPlaylistReadyTimeout(); got != 10*time.Second {
		t.Errorf("expected fallback 10s, got %v", got)
	}
}

func TestHLSManager_getPlaylistPollInterval(t *testing.T) {
	h := &HLSManager{config: HLSManagerConfig{PlaylistPollInterval: 321 * time.Millisecond}}
	if got := h.getPlaylistPollInterval(); got != 321*time.Millisecond {
		t.Errorf("expected 321ms, got %v", got)
	}
	h.config.PlaylistPollInterval = 0
	if got := h.getPlaylistPollInterval(); got != 200*time.Millisecond {
		t.Errorf("expected fallback 200ms, got %v", got)
	}
}

func TestHLSManager_getFFmpegStopTimeout(t *testing.T) {
	h := &HLSManager{config: HLSManagerConfig{FFmpegStopTimeout: 7 * time.Second}}
	if got := h.getFFmpegStopTimeout(); got != 7*time.Second {
		t.Errorf("expected 7s, got %v", got)
	}
	h.config.FFmpegStopTimeout = 0
	if got := h.getFFmpegStopTimeout(); got != 2*time.Second {
		t.Errorf("expected fallback 2s, got %v", got)
	}
}

func TestHLSManager_getViewerHeartbeatTimeout(t *testing.T) {
	h := &HLSManager{config: HLSManagerConfig{ViewerHeartbeatTimeout: 42 * time.Second}}
	if got := h.getViewerHeartbeatTimeout(); got != 42*time.Second {
		t.Errorf("expected 42s, got %v", got)
	}
	h.config.ViewerHeartbeatTimeout = 0
	if got := h.getViewerHeartbeatTimeout(); got != 30*time.Second {
		t.Errorf("expected fallback 30s, got %v", got)
	}
}

func TestHLSManager_GetOrStartSession_FailedCooldown(t *testing.T) {
	h := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())
	inputName := "testinput"
	h.failedInputs[inputName] = time.Now()
	_, err := h.GetOrStartSession(inputName, "rtsp://localhost/relay/testinput")
	if err == nil || !strings.Contains(err.Error(), "cooldown") {
		t.Errorf("expected cooldown error, got %v", err)
	}
}

func TestHLSManager_GetOrStartSession_InvalidInputName(t *testing.T) {
	h := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())
	_, err := h.GetOrStartSession("../badinput", "rtsp://localhost/relay/badinput")
	if err == nil || !strings.Contains(err.Error(), "invalid input name") {
		t.Errorf("expected invalid input name error, got %v", err)
	}
}

func TestHLSManager_GetOrStartSession_RespectsAndCleansCooldown(t *testing.T) {
	h := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())
	inputName := "testinput"
	localURL := "rtsp://localhost/relay/testinput"

	// Simulate a recent failure (within cooldown)
	h.failedInputs[inputName] = time.Now()
	_, err := h.GetOrStartSession(inputName, localURL)
	if err == nil || !strings.Contains(err.Error(), "cooldown") {
		t.Errorf("expected cooldown error, got %v", err)
	}

	// Simulate cooldown expired
	h.failedInputs[inputName] = time.Now().Add(-2 * h.config.FailedCooldown)
	_, err = h.GetOrStartSession(inputName, localURL)
	if err != nil {
		t.Fatalf("expected session to start after cooldown, got %v", err)
	}
	if _, exists := h.failedInputs[inputName]; exists {
		t.Errorf("expected cooldown entry to be cleaned up after expiry, but still present")
	}
}

// --- Mocks for error branches ---
type mockRelayManager struct {
	failStart bool
}

func (m *mockRelayManager) StartInputRelayForConsumer(inputName string) (string, error) {
	if m.failStart {
		return "", errors.New("relay fail")
	}
	return "mockurl", nil
}
func (m *mockRelayManager) StopInputRelayForConsumer(inputName string) {}

// Satisfy the interface expected by HLSManager
var _ interface {
	StartInputRelayForConsumer(string) (string, error)
	StopInputRelayForConsumer(string)
} = &mockRelayManager{}

type testFFmpegProcess struct{ startErr error }

func (m *testFFmpegProcess) Start() error                      { return m.startErr }
func (m *testFFmpegProcess) Stop(timeout time.Duration) error  { return nil }
func (m *testFFmpegProcess) Wait() error                       { return nil }
func (m *testFFmpegProcess) GetLastOutputLines(n int) []string { return nil }
func (p *testFFmpegProcess) GetBitrate() (float64, bool)       { return 0, false } // GetBitrate returns the last parsed bitrate (kbps) and true if available
// GetPID returns 0 for testFFmpegProcess
func (p *testFFmpegProcess) GetPID() int { return 0 }

func TestHLSManager_GetOrStartSession_InputRelayFail(t *testing.T) {
	h := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())
	h.relayManager = &mockRelayManager{failStart: true}
	_, err := h.GetOrStartSession("inputfail", "rtsp://localhost/relay/inputfail")
	if err == nil || !strings.Contains(err.Error(), "failed to start input relay") {
		t.Errorf("expected input relay fail error, got %v", err)
	}
}

func TestHLSManager_GetOrStartSession_TempDirFail(t *testing.T) {
	h := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())
	// Use a non-existent base dir to force MkdirTemp failure
	h.config.PlaylistBaseDir = "/nonexistent/dir/shouldfail"
	_, err := h.GetOrStartSession("input", "rtsp://localhost/relay/input")
	if err == nil || !strings.Contains(err.Error(), "failed to create temp dir") {
		t.Errorf("expected temp dir fail error, got %v", err)
	}
}

func TestHLSManager_GetOrStartSession_FFmpegFail(t *testing.T) {
	h := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())
	// Patch HLSManager to use a test double for FFmpegProcess creation
	h.newFFmpegProcess = func(ctx context.Context, args ...string) (ffmpegProcess, error) {
		return nil, errors.New("ffmpeg create fail")
	}
	_, err := h.GetOrStartSession("input", "rtsp://localhost/relay/input")
	if err == nil || !strings.Contains(err.Error(), "failed to create ffmpeg process") {
		t.Errorf("expected ffmpeg create fail error, got %v", err)
	}
	// Now test ffmpeg Start() fail
	h.newFFmpegProcess = func(ctx context.Context, args ...string) (ffmpegProcess, error) {
		return &testFFmpegProcess{startErr: errors.New("ffmpeg start fail")}, nil
	}
	_, err = h.GetOrStartSession("input2", "rtsp://localhost/relay/input2")
	if err == nil || !strings.Contains(err.Error(), "failed to start ffmpeg") {
		t.Errorf("expected ffmpeg start fail error, got %v", err)
	}
}

// --- Test for Shutdown ---
type shutdownMockRelay struct{ stopped []string }

func (m *shutdownMockRelay) StartInputRelayForConsumer(string) (string, error) { return "", nil }
func (m *shutdownMockRelay) StopInputRelayForConsumer(inputName string) {
	m.stopped = append(m.stopped, inputName)
}

type shutdownMockProc struct {
	stopped bool
	waited  bool
}

func (p *shutdownMockProc) Start() error                      { return nil }
func (p *shutdownMockProc) Stop(timeout time.Duration) error  { p.stopped = true; return nil }
func (p *shutdownMockProc) Wait() error                       { p.waited = true; return nil }
func (p *shutdownMockProc) GetLastOutputLines(n int) []string { return nil }
func (p *shutdownMockProc) GetBitrate() (float64, bool)       { return 0, false } // GetBitrate returns the last parsed bitrate (kbps) and true if available
// GetPID returns 0 for shutdownMockProc
func (p *shutdownMockProc) GetPID() int { return 0 }

func TestHLSManager_Shutdown(t *testing.T) {
	logr := newTestLogger()
	mgr := NewHLSManager(minimalHLSManagerConfig(), logr)
	relay := &shutdownMockRelay{}
	mgr.relayManager = relay

	sess := &HLSSession{
		InputName:  "foo",
		IsConsumer: true,
		Dir:        t.TempDir(),
		Proc:       &shutdownMockProc{},
	}
	mgr.sessions["foo"] = sess

	mgr.Shutdown()

	if len(relay.stopped) != 1 || relay.stopped[0] != "foo" {
		t.Errorf("expected relay StopInputRelayForConsumer to be called for 'foo', got %v", relay.stopped)
	}
	proc, ok := sess.Proc.(*shutdownMockProc)
	if !ok || !proc.stopped {
		t.Error("expected ffmpeg process Stop to be called")
	}
	if !ok || !proc.waited {
		t.Error("expected ffmpeg process Wait to be called")
	}
	if _, err := os.Stat(sess.Dir); !os.IsNotExist(err) {
		t.Error("expected session dir to be removed on shutdown")
	}
}

func TestHLSManager_WriteEndlistToAll(t *testing.T) {
	logr := newTestLogger()
	mgr := NewHLSManager(minimalHLSManagerConfig(), logr)
	dir := t.TempDir()
	playlistPath := filepath.Join(dir, "index.m3u8")
	// Write a playlist without ENDLIST
	os.WriteFile(playlistPath, []byte("#EXTM3U\n#EXT-X-VERSION:3\n"), 0644)
	sess := &HLSSession{
		InputName: "foo",
		Dir:       dir,
		ViewerIDs: make(map[string]time.Time),
		Proc:      &shutdownMockProc{}, // Ensure Proc is non-nil for cleanup safety
	}
	mgr.sessions["foo"] = sess

	mgr.WriteEndlistToAll()
	data, err := os.ReadFile(playlistPath)
	if err != nil {
		t.Fatalf("failed to read playlist: %v", err)
	}
	if !strings.Contains(string(data), "#EXT-X-ENDLIST") {
		t.Error("expected #EXT-X-ENDLIST to be written to playlist")
	}

	// Test idempotency: call again, should not duplicate ENDLIST
	mgr.WriteEndlistToAll()
	data2, _ := os.ReadFile(playlistPath)
	if strings.Count(string(data2), "#EXT-X-ENDLIST") != 1 {
		t.Error("expected only one #EXT-X-ENDLIST after repeated calls")
	}
}

func TestCheckFailedCooldownDeletesExpired(t *testing.T) {
	mgr := &HLSManager{
		failedInputs:   map[string]time.Time{"foo": time.Now().Add(-2 * time.Second)},
		failedCooldown: 1 * time.Second,
		logger:         newTestLogger(),
	}
	err := mgr.checkFailedCooldown("foo")
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if _, exists := mgr.failedInputs["foo"]; exists {
		t.Errorf("expected entry to be deleted after cooldown, but still present")
	}
}

func TestTryFsnotifyPlaylistReady_Integration(t *testing.T) {
	dir := t.TempDir()
	playlistPath := filepath.Join(dir, "index.m3u8")
	mgr := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())

	// Case 1: File is created and written after a short delay (should return true)
	go func() {
		time.Sleep(100 * time.Millisecond)
		os.WriteFile(playlistPath, []byte("#EXTM3U\n"), 0644)
	}()
	ready := mgr.tryFsnotifyPlaylistReady(playlistPath)
	if !ready {
		t.Error("expected playlist to become ready after file write")
	}

	// Case 2: File is never created (should timeout and return false)
	playlistPath2 := filepath.Join(dir, "index2.m3u8")
	mgr2 := NewHLSManager(HLSManagerConfig{
		CleanupInterval:        1 * time.Second,
		SessionTimeout:         1 * time.Second,
		FailedCooldown:         1 * time.Second,
		PlaylistReadyTimeout:   200 * time.Millisecond, // short timeout
		PlaylistPollInterval:   50 * time.Millisecond,
		PlaylistPollAttempts:   2,
		ViewerHeartbeatTimeout: 1 * time.Second,
		FFmpegStopTimeout:      1 * time.Second,
		PlaylistBaseDir:        dir,
	}, newTestLogger())
	ready2 := mgr2.tryFsnotifyPlaylistReady(playlistPath2)
	if ready2 {
		t.Error("expected playlist to not become ready when file is never created")
	}
}

func TestTryFsnotifyPlaylistReady_AllBranches(t *testing.T) {
	dir := t.TempDir()
	playlistPath := filepath.Join(dir, "index.m3u8")
	mgr := NewHLSManager(HLSManagerConfig{
		CleanupInterval:        1 * time.Second,
		SessionTimeout:         1 * time.Second,
		FailedCooldown:         1 * time.Second,
		PlaylistReadyTimeout:   500 * time.Millisecond,
		PlaylistPollInterval:   50 * time.Millisecond,
		PlaylistPollAttempts:   3,
		ViewerHeartbeatTimeout: 1 * time.Second,
		FFmpegStopTimeout:      1 * time.Second,
		PlaylistBaseDir:        dir,
	}, newTestLogger())

	// Case 1: File exists and is non-empty before call (stat branch)
	os.WriteFile(playlistPath, []byte("#EXTM3U\n"), 0644)
	if !mgr.tryFsnotifyPlaylistReady(playlistPath) {
		t.Error("expected playlist to be ready immediately (stat branch)")
	}
	os.Remove(playlistPath)

	// Case 2: File is created after a delay (event branch)
	go func() {
		time.Sleep(100 * time.Millisecond)
		os.WriteFile(playlistPath, []byte("#EXTM3U\n"), 0644)
	}()
	if !mgr.tryFsnotifyPlaylistReady(playlistPath) {
		t.Error("expected playlist to become ready after file write (event branch)")
	}
	os.Remove(playlistPath)

	// Case 3: File is created but empty, then written to (event+stat branch)
	os.WriteFile(playlistPath, []byte(""), 0644)
	go func() {
		time.Sleep(100 * time.Millisecond)
		os.WriteFile(playlistPath, []byte("#EXTM3U\n"), 0644)
	}()
	if !mgr.tryFsnotifyPlaylistReady(playlistPath) {
		t.Error("expected playlist to become ready after file write (event+stat branch)")
	}
	os.Remove(playlistPath)

	// Case 4: Timeout branch (file never created)
	playlistPath2 := filepath.Join(dir, "index2.m3u8")
	if mgr.tryFsnotifyPlaylistReady(playlistPath2) {
		t.Error("expected playlist to not become ready (timeout branch)")
	}
}

// --- Test for serveHLSCheckViewer coverage ---
func TestServeHLSCheckViewer_AllBranches(t *testing.T) {
	mgr := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())
	inputName := "testinput"

	// 1. Invalid input name (should return 404, not 400, if session not found first)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/index.m3u8", nil)
	mgr.ServeHLS(w, r, "../badinput", "index.m3u8", "rtsp://localhost/relay/testinput")
	if w.Result().StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for invalid input name, got %d", w.Result().StatusCode)
	}

	// 2. Session not found (should return 404)
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/index.m3u8", nil)
	mgr.ServeHLS(w, r, "missinginput", "index.m3u8", "rtsp://localhost/relay/testinput")
	if w.Result().StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for missing session, got %d", w.Result().StatusCode)
	}

	// 3. Session with no ViewerManager (should return 404)
	ensureSessionReady(mgr, inputName, nil)
	w = httptest.NewRecorder()
	mgr.ServeHLS(w, r, inputName, "index.m3u8", "rtsp://localhost/relay/testinput")
	if w.Result().StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for missing ViewerManager, got %d", w.Result().StatusCode)
	}

	// 4. Session with ViewerManager, viewerID not found (should return 404)
	goodVM := &MapViewerManager{sess: nil}
	ensureSessionReady(mgr, inputName, goodVM)
	goodVM.sess = mgr.sessions[inputName]
	w = httptest.NewRecorder()
	mgr.ServeHLS(w, r, inputName, "index.m3u8", "rtsp://localhost/relay/testinput")
	if w.Result().StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for viewerID not found, got %d", w.Result().StatusCode)
	}

	// 5. Add viewer, then expire it (should return 410)
	viewerID, err := mgr.AddViewer(inputName)
	if err != nil {
		t.Fatalf("AddViewer failed: %v", err)
	}
	mgr.sessions[inputName].ViewerIDs[viewerID] = time.Now().Add(-time.Hour)
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/index.m3u8?viewerID="+viewerID, nil)
	mgr.ServeHLS(w, r, inputName, "index.m3u8", "rtsp://localhost/relay/testinput")
	if w.Result().StatusCode != http.StatusGone {
		t.Errorf("expected 410 for expired viewerID, got %d", w.Result().StatusCode)
	}

	// 6. Add viewer, valid (should return 200)
	viewerID, err = mgr.AddViewer(inputName)
	if err != nil {
		t.Fatalf("AddViewer failed: %v", err)
	}
	mgr.sessions[inputName].ViewerIDs[viewerID] = time.Now().Add(time.Hour)
	sess := mgr.sessions[inputName]
	playlistPath := filepath.Join(sess.Dir, "index.m3u8")
	os.WriteFile(playlistPath, []byte("#EXTM3U\n#EXT-X-VERSION:3\n"), 0644)
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/index.m3u8?viewerID="+viewerID, nil)
	mgr.ServeHLS(w, r, inputName, "index.m3u8", "rtsp://localhost/relay/testinput")
	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200 for success with viewerID, got %d", w.Result().StatusCode)
	}

	// 7. Success path without viewerID (should return 200)
	ensureSessionReady(mgr, inputName, goodVM)
	goodVM.sess = mgr.sessions[inputName]
	playlistPath = filepath.Join(goodVM.sess.Dir, "index.m3u8")
	os.WriteFile(playlistPath, []byte("#EXTM3U\n#EXT-X-VERSION:3\n"), 0644)
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/index.m3u8", nil)
	mgr.ServeHLS(w, r, inputName, "index.m3u8", "rtsp://localhost/relay/testinput")
	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200 for success without viewerID, got %d", w.Result().StatusCode)
	}
}

func TestServeHLSCheckViewer_AllBranches_Coverage(t *testing.T) {
	mgr := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())
	inputName := "testinput"
	// No session: should return 410 if viewerID is present
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/index.m3u8?viewerID=foo", nil)
	mgr.ServeHLS(w, r, inputName, "index.m3u8", "")
	if w.Result().StatusCode != http.StatusGone {
		t.Errorf("expected 410 for missing session with viewerID, got %d", w.Result().StatusCode)
	}
	// Session with no ViewerManager
	ensureSessionReady(mgr, inputName, nil)
	w = httptest.NewRecorder()
	mgr.ServeHLS(w, r, inputName, "index.m3u8", "")
	if w.Result().StatusCode != http.StatusGone {
		t.Errorf("expected 410 for missing ViewerManager with viewerID, got %d", w.Result().StatusCode)
	}
	// Session with ViewerManager, viewerID not found
	goodVM := &MapViewerManager{sess: nil}
	ensureSessionReady(mgr, inputName, goodVM)
	goodVM.sess = mgr.sessions[inputName]
	w = httptest.NewRecorder()
	mgr.ServeHLS(w, r, inputName, "index.m3u8", "")
	if w.Result().StatusCode != http.StatusGone {
		t.Errorf("expected 410 for viewerID not found, got %d", w.Result().StatusCode)
	}
	// Add viewer, then expire it
	viewerID, _ := mgr.AddViewer(inputName)
	mgr.sessions[inputName].ViewerIDs[viewerID] = time.Now().Add(-time.Hour)
	w = httptest.NewRecorder()
	mgr.ServeHLS(w, r, inputName, "index.m3u8?viewerID="+viewerID, "")
	if w.Result().StatusCode != http.StatusGone {
		t.Errorf("expected 410 for expired viewerID, got %d", w.Result().StatusCode)
	}
	// Add viewer, valid
	viewerID, _ = mgr.AddViewer(inputName)
	mgr.sessions[inputName].ViewerIDs[viewerID] = time.Now().Add(time.Hour)
	playlistPath := filepath.Join(mgr.sessions[inputName].Dir, "index.m3u8")
	os.WriteFile(playlistPath, []byte("#EXTM3U\n#EXT-X-VERSION:3\n"), 0644)
	mgr.sessions[inputName].ViewerManager = &MapViewerManager{sess: mgr.sessions[inputName]} // Ensure ViewerManager is set
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/index.m3u8?viewerID="+viewerID, nil)
	mgr.ServeHLS(w, r, inputName, "index.m3u8", "")
	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200 for valid viewerID, got %d", w.Result().StatusCode)
	}
}

func TestHLSManager_CleanupLoop_RemovesStaleSessions(t *testing.T) {
	mgr := NewHLSManager(HLSManagerConfig{
		CleanupInterval:        50 * time.Millisecond,
		SessionTimeout:         50 * time.Millisecond,
		FailedCooldown:         1 * time.Second,
		PlaylistReadyTimeout:   1 * time.Second,
		PlaylistPollInterval:   100 * time.Millisecond,
		PlaylistPollAttempts:   1,
		ViewerHeartbeatTimeout: 10 * time.Millisecond,
		FFmpegStopTimeout:      1 * time.Second,
		PlaylistBaseDir:        os.TempDir(),
	}, newTestLogger())
	inputName := "cleanupinput"
	sess := &HLSSession{
		InputName:  inputName,
		Dir:        t.TempDir(),
		Ready:      true,
		ViewerIDs:  map[string]time.Time{"v1": time.Now().Add(-time.Hour), "v2": time.Now().Add(time.Hour)},
		LastAccess: time.Now().Add(-time.Second),
		Proc:       &shutdownMockProc{},
	}
	mgr.mu.Lock()
	mgr.sessions[inputName] = sess
	mgr.mu.Unlock()
	// Wait for cleanupLoop to run
	time.Sleep(200 * time.Millisecond)
	mgr.mu.Lock()
	_, exists := mgr.sessions[inputName]
	mgr.mu.Unlock()
	if exists {
		t.Errorf("expected session to be removed by cleanupLoop")
	}
	// Test non-consumer session removal
	inputName2 := "cleanupinput2"
	sess2 := &HLSSession{
		InputName:  inputName2,
		Dir:        t.TempDir(),
		Ready:      true,
		ViewerIDs:  map[string]time.Time{},
		LastAccess: time.Now().Add(-time.Second),
		Proc:       &shutdownMockProc{},
	}
	mgr.mu.Lock()
	mgr.sessions[inputName2] = sess2
	mgr.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	mgr.mu.Lock()
	_, exists2 := mgr.sessions[inputName2]
	mgr.mu.Unlock()
	if exists2 {
		t.Errorf("expected non-consumer session to be removed by cleanupLoop")
	}
}

func TestHLSManager_GetOrStartSession_ErrorBranches(t *testing.T) {
	mgr := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())
	// Recent failure (cooldown)
	mgr.failedInputs["failinput"] = time.Now()
	_, err := mgr.GetOrStartSession("failinput", "rtsp://localhost/relay/failinput")
	if err == nil || !strings.Contains(err.Error(), "cooldown") {
		t.Errorf("expected cooldown error, got %v", err)
	}
	// Invalid input name
	_, err = mgr.GetOrStartSession("../badinput", "rtsp://localhost/relay/badinput")
	if err == nil || !strings.Contains(err.Error(), "invalid input name") {
		t.Errorf("expected invalid input name error, got %v", err)
	}
	// Session already exists
	inputName := "existsinput"
	mgr.sessions[inputName] = &HLSSession{InputName: inputName, Ready: true, Proc: &shutdownMockProc{}}
	sess, err := mgr.GetOrStartSession(inputName, "rtsp://localhost/relay/existsinput")
	if err != nil || sess == nil {
		t.Errorf("expected existing session, got %v, %v", sess, err)
	}
	// RelayManager error
	mgr2 := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())
	mgr2.relayManager = &mockRelayManager{failStart: true}
	_, err = mgr2.GetOrStartSession("failrelay", "rtsp://localhost/relay/failrelay")
	if err == nil || !strings.Contains(err.Error(), "failed to start input relay") {
		t.Errorf("expected relay fail error, got %v", err)
	}
	// Temp dir error
	mgr3 := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())
	mgr3.config.PlaylistBaseDir = "/nonexistent/dir/shouldfail"
	_, err = mgr3.GetOrStartSession("faildir", "rtsp://localhost/relay/faildir")
	if err == nil || !strings.Contains(err.Error(), "failed to create temp dir") {
		t.Errorf("expected temp dir fail error, got %v", err)
	}
	// FFmpegProcess error
	mgr4 := NewHLSManager(minimalHLSManagerConfig(), newTestLogger())
	mgr4.newFFmpegProcess = func(ctx context.Context, args ...string) (ffmpegProcess, error) {
		return nil, errors.New("ffmpeg create fail")
	}
	_, err = mgr4.GetOrStartSession("failffmpeg", "rtsp://localhost/relay/failffmpeg")
	if err == nil || !strings.Contains(err.Error(), "failed to create ffmpeg process") {
		t.Errorf("expected ffmpeg create fail error, got %v", err)
	}
	// FFmpeg Start error
	mgr4.newFFmpegProcess = func(ctx context.Context, args ...string) (ffmpegProcess, error) {
		return &testFFmpegProcess{startErr: errors.New("ffmpeg start fail")}, nil
	}
	_, err = mgr4.GetOrStartSession("failffmpeg2", "rtsp://localhost/relay/failffmpeg2")
	if err == nil || !strings.Contains(err.Error(), "failed to start ffmpeg") {
		t.Errorf("expected ffmpeg start fail error, got %v", err)
	}
}

// ensureSessionReady ensures the session for inputName exists and is marked Ready, and sets the session's ViewerManager.
func ensureSessionReady(mgr *HLSManager, inputName string, vm ViewerManager) {
	delete(mgr.sessions, inputName)
	sess, _ := mgr.GetOrStartSession(inputName, "rtsp://localhost/relay/"+inputName)
	sess.Ready = true
	sess.ViewerManager = vm
	if sess.Proc == nil {
		sess.Proc = &shutdownMockProc{} // Ensure Proc is always non-nil for cleanup safety
	}
	if sess2, ok := mgr.sessions[inputName]; !ok || sess2 == nil || !sess2.Ready || sess2.ViewerManager != vm {
		panic("ensureSessionReady: session not present, not ready, or viewer manager not set")
	}
}
