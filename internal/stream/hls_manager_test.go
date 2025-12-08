package stream

import (
	"context"
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

	mgr := NewHLSManager(newTestLogger(), minimalHLSManagerConfig(), &mockStreamProvider{}, nil)
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
	logr := logger.NewLoggerWithConfig("debug", "")
	mgr := NewHLSManager(logr, minimalHLSManagerConfig(), nil, nil)
	inputName := "missinginput"
	file := "index.m3u8"

	// Call ServeHLS multiple times, expect 404 for missing input
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/index.m3u8", nil)
		mgr.ServeHLS(w, r, inputName, file, "")
		if w.Result().StatusCode != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w.Result().StatusCode)
		}
		body, _ := io.ReadAll(w.Result().Body)
		if !strings.Contains(string(body), "HLS session not found") {
			t.Errorf("expected 'HLS session not found' error message, got: %s", string(body))
		}
	}
	// We do not check log count here; logger should handle rate limiting
}

func TestHLSManager_ConcurrentAPI(t *testing.T) {
	t.Parallel()
	logr := logger.NewLogger()
	mgr := NewHLSManager(logr, minimalHLSManagerConfig(), &mockStreamProvider{}, nil)
	// mgr.streamProvider = NewRelayManager(logr, dir, "")

	num := 10
	var wg sync.WaitGroup
	inputNames := make([]string, num)
	for i := 0; i < num; i++ {
		inputNames[i] = "input" + string(rune('A'+i))
	}

	timeout := time.After(10 * time.Second)
	done := make(chan struct{})

	// Background goroutine to create dummy playlists for active sessions
	// This is needed because ServeHLS waits for playlist readiness
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				mgr.mu.RLock()
				for _, sess := range mgr.sessions {
					// Check if playlist exists
					playlistPath := filepath.Join(sess.Dir, "index.m3u8")
					if _, err := os.Stat(playlistPath); os.IsNotExist(err) {
						// Create dummy playlist
						_ = os.WriteFile(playlistPath, []byte("#EXTM3U\n"), 0644)
					}
				}
				mgr.mu.RUnlock()
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()

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
		SegmentDuration:        2 * time.Second,
		PlaylistSize:           6,
		FFmpegPreset:           "ultrafast",
	}
}

func newTestLogger() *logger.Logger {
	return logger.NewLoggerWithConfig("debug", os.DevNull)
}

func TestNewHLSManager_CreatesManager(t *testing.T) {
	h := NewHLSManager(newTestLogger(), minimalHLSManagerConfig(), &mockStreamProvider{}, nil)
	if h == nil {
		t.Fatal("expected non-nil HLSManager")
	}
	if h.sessions == nil {
		t.Error("expected sessions map to be initialized")
	}
}

func TestHLSManager_GetOrStartSession_Basic(t *testing.T) {
	h := NewHLSManager(newTestLogger(), minimalHLSManagerConfig(), &mockStreamProvider{}, nil)
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
	h := NewHLSManager(newTestLogger(), minimalHLSManagerConfig(), &mockStreamProvider{}, nil)
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

func TestHLSManager_GetOrStartSession_FailedCooldown(t *testing.T) {
	h := NewHLSManager(newTestLogger(), minimalHLSManagerConfig(), &mockStreamProvider{}, nil)
	inputName := "testinput"
	h.failedInputs[inputName] = time.Now()
	_, err := h.GetOrStartSession(inputName, "rtsp://localhost/relay/testinput")
	if err == nil || !strings.Contains(err.Error(), "cooldown") {
		t.Errorf("expected cooldown error, got %v", err)
	}
}

func TestHLSManager_GetOrStartSession_InvalidInputName(t *testing.T) {
	h := NewHLSManager(newTestLogger(), minimalHLSManagerConfig(), &mockStreamProvider{}, nil)
	_, err := h.GetOrStartSession("../badinput", "rtsp://localhost/relay/badinput")
	if err == nil || !strings.Contains(err.Error(), "invalid input name") {
		t.Errorf("expected invalid input name error, got %v", err)
	}
}

// --- Mocks for error branches ---

type testFFmpegProcess struct{ startErr error }

func (m *testFFmpegProcess) Start(ctx context.Context) error                       { return m.startErr }
func (m *testFFmpegProcess) Stop(ctx context.Context, timeout time.Duration) error { return nil }
func (m *testFFmpegProcess) Wait() error                                           { return nil }
func (m *testFFmpegProcess) GetLastOutputLines(n int) []string                     { return nil }
func (p *testFFmpegProcess) GetBitrate() (float64, bool)                           { return 0, false }
func (p *testFFmpegProcess) GetPID() int                                           { return 0 }
func (p *testFFmpegProcess) GetOutput() string                                     { return "" }
func (p *testFFmpegProcess) GetSpeed() (float64, time.Time)                        { return 0, time.Time{} }
func (p *testFFmpegProcess) OutputChannel() <-chan string                          { return nil }

// --- Test for Shutdown ---
type shutdownMockRelay struct{ stopped []string }

func (m *shutdownMockRelay) StartInputRelayForConsumer(string) (string, error) { return "", nil }
func (m *shutdownMockRelay) StopInputRelayForConsumer(inputName, outputURL string) {
	m.stopped = append(m.stopped, inputName)
}

type shutdownMockProc struct {
	stopped bool
	waited  bool
}

func (p *shutdownMockProc) Start(ctx context.Context) error { return nil }
func (p *shutdownMockProc) Stop(ctx context.Context, timeout time.Duration) error {
	p.stopped = true
	return nil
}
func (p *shutdownMockProc) Wait() error                       { p.waited = true; return nil }
func (p *shutdownMockProc) GetLastOutputLines(n int) []string { return nil }
func (p *shutdownMockProc) GetBitrate() (float64, bool)       { return 0, false } // GetBitrate returns the last parsed bitrate (kbps) and true if available
// GetPID returns 0 for shutdownMockProc
func (p *shutdownMockProc) GetPID() int                    { return 0 }
func (p *shutdownMockProc) GetOutput() string              { return "" }
func (p *shutdownMockProc) GetSpeed() (float64, time.Time) { return 0, time.Time{} }
func (p *shutdownMockProc) OutputChannel() <-chan string   { return nil }

func TestCheckFailedCooldownDeletesExpired(t *testing.T) {
	mgr := &HLSManager{
		failedInputs: map[string]time.Time{"foo": time.Now().Add(-2 * time.Second)},
		config:       HLSManagerConfig{FailedCooldown: 1 * time.Second},
		Logger:       newTestLogger(),
	}
	err := mgr.checkFailedCooldown("foo")
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if _, exists := mgr.failedInputs["foo"]; exists {
		t.Errorf("expected entry to be deleted after cooldown, but still present")
	}
}

// --- Test for serveHLSCheckViewer coverage ---
func TestServeHLSCheckViewer_AllBranches(t *testing.T) {
	mgr := NewHLSManager(newTestLogger(), minimalHLSManagerConfig(), &mockStreamProvider{}, nil)
	inputName := "testinput"

	// 1. Invalid input name (should return 404 for index.m3u8)
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
		t.Errorf("expected 200 for valid viewerID, got %d", w.Result().StatusCode)
	}
	body, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), "#EXTM3U") {
		t.Errorf("expected playlist content, got: %s", string(body))
	}
}

func TestServeHLSCheckViewer_AllBranches_Coverage(t *testing.T) {
	mgr := NewHLSManager(newTestLogger(), minimalHLSManagerConfig(), &mockStreamProvider{}, nil)
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

// ensureSessionReady ensures the session for inputName exists and is marked Ready, and sets the session's ViewerManager.
func ensureSessionReady(mgr *HLSManager, inputName string, vm *MapViewerManager) {
	delete(mgr.sessions, inputName)
	// Use minimal config for test session
	testConfig := minimalHLSManagerConfig()
	testConfig.PlaylistBaseDir = mgr.config.PlaylistBaseDir // Preserve the temp dir from the manager under test

	// Create a temporary manager just to get the session logic, or manually construct session
	// Since GetOrStartSession is a method on HLSManager, we just call it on the existing mgr
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
