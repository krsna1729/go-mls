package stream

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

	mgr := &HLSManager{
		sessions:        make(map[string]*HLSSession),
		ffmpegPath:      "/bin/true", // not used
		cleanupInterval: time.Minute,
		sessionTimeout:  time.Minute,
		relayManager:    nil, // no logging needed for this test
	}
	inputName := "testinput"
	sess := &HLSSession{
		InputName: inputName,
		Dir:       dir,
		Ready:     true,
		ViewerIDs: make(map[string]time.Time),
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
	// Create a logger that writes to the test buffer
	logr := logger.NewLoggerWithWriter(&buf)
	mgr := &HLSManager{
		sessions:            make(map[string]*HLSSession),
		ffmpegPath:          "/bin/true",
		cleanupInterval:     time.Minute,
		sessionTimeout:      time.Minute,
		relayManager:        &RelayManager{Logger: logr},
		notFoundLogTimes:    make(map[string]time.Time),
		notFoundLogInterval: 100 * time.Millisecond, // short for test
	}
	inputName := "missinginput"
	file := "index.m3u8"

	start := time.Now()
	// Rapidly call ServeHLS multiple times
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/index.m3u8", nil)
		mgr.ServeHLS(w, r, inputName, file, "")
		if w.Result().StatusCode != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w.Result().StatusCode)
		}
	}
	warns := strings.Count(buf.String(), "[WARN]")
	if warns != 1 {
		t.Errorf("expected 1 warning log in first burst, got %d", warns)
	}

	// Wait for interval to pass, then call again
	time.Sleep(mgr.notFoundLogInterval + 20*time.Millisecond)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/index.m3u8", nil)
	mgr.ServeHLS(w, r, inputName, file, "")
	warns = strings.Count(buf.String(), "[WARN]")
	if warns != 2 {
		t.Errorf("expected 2 warning logs after interval, got %d", warns)
	}

	// Timeout safety: ensure test does not hang
	if time.Since(start) > 2*time.Second {
		t.Fatal("test took too long, possible deadlock or leak")
	}
}
