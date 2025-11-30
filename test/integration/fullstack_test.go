package integration_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go-mls/internal/logger"
	"go-mls/internal/stream"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fullStackTestEnv holds the components for a full-stack integration test
type fullStackTestEnv struct {
	tempDir      string
	relayMgr     *stream.RelayManager
	recordingMgr *stream.RecordingManager
	hlsMgr       *stream.HLSManager
	ts           *httptest.Server
}

// doRequest performs an HTTP request against the test server
func (e *fullStackTestEnv) doRequest(method, path string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(jsonData)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, reqBody)
	if err != nil {
		return nil, err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return http.DefaultClient.Do(req)
}

// setupFullStackTestEnv initializes the test environment and components
func setupFullStackTestEnv(t *testing.T) *fullStackTestEnv {
	// Skip if test file doesn't exist
	testFile := filepath.Join("..", "..", "testdata", "testsrc.mp4")
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Skipf("Skipping integration test: %s not found", testFile)
	}

	// Setup test environment
	tempDir := t.TempDir()
	log := logger.NewLogger()

	// Copy test file to temp recDir
	destPath := filepath.Join(tempDir, "testsrc.mp4")
	srcData, err := os.ReadFile(testFile)
	require.NoError(t, err, "Failed to read test file")
	err = os.WriteFile(destPath, srcData, 0644)
	require.NoError(t, err, "Failed to copy test file to temp dir")

	// === Setup Components ===
	rtspServer := stream.NewRTSPServerManager(log, "127.0.0.1", 0) // Use port 0 for random port
	err = rtspServer.Start()
	require.NoError(t, err, "Failed to start RTSP server")
	t.Cleanup(func() { rtspServer.Stop() })

	relayMgr := stream.NewRelayManager(log, tempDir, "error")
	relayMgr.SetRTSPServer(rtspServer)

	recordingMgr := stream.NewRecordingManager(log, tempDir, relayMgr)
	t.Cleanup(func() { recordingMgr.Shutdown() })

	hlsMgr := stream.NewHLSManager(stream.HLSManagerConfig{
		CleanupInterval:        30 * time.Second,
		SessionTimeout:         60 * time.Second,
		FailedCooldown:         10 * time.Second,
		PlaylistReadyTimeout:   10 * time.Second,
		PlaylistPollInterval:   200 * time.Millisecond,
		PlaylistPollAttempts:   50,
		ViewerHeartbeatTimeout: 30 * time.Second,
		FFmpegStopTimeout:      2 * time.Second,
		PlaylistBaseDir:        "/tmp",
	}, log)
	hlsMgr.SetRelayManager(relayMgr)
	t.Cleanup(func() { hlsMgr.Shutdown() })

	relayMgr.HLSManager = hlsMgr
	relayMgr.RecordingManager = recordingMgr

	// === Setup HTTP Server with API Handlers ===
	mux := http.NewServeMux()

	// Relay APIs
	mux.HandleFunc("/api/relay/start", stream.ApiStartRelay(relayMgr))
	mux.HandleFunc("/api/relay/stop", stream.ApiStopRelay(relayMgr))

	// Recording APIs
	mux.HandleFunc("/api/recording/start", stream.ApiStartRecording(recordingMgr))
	mux.HandleFunc("/api/recording/stop", stream.ApiStopRecording(recordingMgr))

	// HLS APIs
	mux.HandleFunc("/api/relay/hls/start-viewer", stream.ApiStartHLSViewer(hlsMgr, relayMgr))
	mux.HandleFunc("/api/relay/hls/stop-viewer", stream.ApiStopHLSViewer(hlsMgr, relayMgr))

	// Create test HTTP server
	ts := httptest.NewServer(mux)
	t.Cleanup(func() { ts.Close() })

	return &fullStackTestEnv{
		tempDir:      tempDir,
		relayMgr:     relayMgr,
		recordingMgr: recordingMgr,
		hlsMgr:       hlsMgr,
		ts:           ts,
	}
}

// execute runs the given function n times, either sequentially or concurrently
func execute(n int, concurrent bool, fn func(i int)) {
	if concurrent {
		var wg sync.WaitGroup
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func(idx int) {
				defer wg.Done()
				fn(idx)
			}(i)
		}
		wg.Wait()
	} else {
		for i := 0; i < n; i++ {
			fn(i)
		}
	}
}

// runFullStackLifecycle runs the full integration test lifecycle
func runFullStackLifecycle(t *testing.T, concurrent bool) {
	env := setupFullStackTestEnv(t)
	relayMgr := env.relayMgr
	hlsMgr := env.hlsMgr
	tempDir := env.tempDir
	doRequest := env.doRequest

	// === Phase 1: Start Consumers ===
	t.Logf("=== Phase 1: Starting Consumers (Concurrent: %v) ===", concurrent)
	t.Log("Target: 5 outputs + 1 recording + 3 HLS viewers = RefCount 7")

	inputURL := "file://testsrc.mp4"
	inputName := "TestInput"

	// Start 5 output relays
	t.Log("Step 1: Start 5 output relays")
	execute(5, concurrent, func(i int) {
		outputFile := filepath.Join(tempDir, fmt.Sprintf("output%d.flv", i))
		resp, err := doRequest("POST", "/api/relay/start", map[string]interface{}{
			"input_name":  inputName,
			"input_url":   inputURL,
			"output_name": fmt.Sprintf("Output%d", i),
			"output_url":  "file://" + filepath.Base(outputFile),
		})
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, "Output %d should start successfully", i)
		resp.Body.Close()
	})

	// Verify refcount = 5
	status, refCount, exists := relayMgr.InputRelays.GetRelayStatus(inputURL)
	require.True(t, exists, "Input relay should exist")
	assert.Equal(t, 5, refCount, "RefCount should be 5 after 5 outputs")
	assert.Equal(t, stream.InputRunning, status, "Input should be running")

	// Start Recording
	t.Log("Step 2: Start recording")
	resp, err := doRequest("POST", "/api/recording/start", map[string]interface{}{
		"name":   inputName,
		"source": inputURL,
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	time.Sleep(1 * time.Second)
	status, refCount, _ = relayMgr.InputRelays.GetRelayStatus(inputURL)
	assert.Equal(t, 6, refCount, "RefCount should be 6 after recording starts")

	// Start 3 HLS viewers (HLS session counts as 1 consumer)
	t.Log("Step 3: Start 3 HLS viewers (HLS = 1 consumer)")
	viewerIDs := make([]string, 3)
	var viewerMu sync.Mutex // Protect viewerIDs slice during concurrent access

	execute(3, concurrent, func(i int) {
		resp, err := doRequest("POST", "/api/relay/hls/start-viewer", map[string]interface{}{
			"input_name": inputName,
		})
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var hlsResp map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&hlsResp)
		resp.Body.Close()
		require.NoError(t, err)

		viewerID, ok := hlsResp["viewer_id"].(string)
		require.True(t, ok && viewerID != "", "viewer_id should be present")

		viewerMu.Lock()
		viewerIDs[i] = viewerID
		viewerMu.Unlock()

		t.Logf("  HLS viewer %d started: %s", i+1, viewerID)
	})

	time.Sleep(1 * time.Second)
	// Refcount should be 7 (5 outputs + 1 recording + 1 HLS session)
	status, refCount, _ = relayMgr.InputRelays.GetRelayStatus(inputURL)
	assert.Equal(t, 7, refCount, "RefCount should be 7 (5 outputs + 1 recording + 1 HLS)")
	assert.Equal(t, stream.InputRunning, status, "Input should be running")

	// === Phase 2: Stop Consumers ===
	t.Log("=== Phase 2: Stopping Consumers ===")

	// Stop all 5 outputs
	t.Log("Step 4: Stop all 5 output relays")
	execute(5, concurrent, func(i int) {
		outputFile := filepath.Join(tempDir, fmt.Sprintf("output%d.flv", i))
		resp, err := doRequest("POST", "/api/relay/stop", map[string]interface{}{
			"input_url":   inputURL,
			"output_url":  "file://" + filepath.Base(outputFile),
			"input_name":  inputName,
			"output_name": fmt.Sprintf("Output%d", i),
		})
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	})

	time.Sleep(500 * time.Millisecond)
	status, refCount, _ = relayMgr.InputRelays.GetRelayStatus(inputURL)
	assert.Equal(t, 2, refCount, "RefCount should be 2 (recording + HLS)")
	assert.Equal(t, stream.InputRunning, status, "Input should still be running")

	// Stop Recording
	t.Log("Step 5: Stop recording")
	resp, err = doRequest("POST", "/api/recording/stop", map[string]interface{}{
		"name":   inputName,
		"source": inputURL,
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	time.Sleep(500 * time.Millisecond)
	status, refCount, _ = relayMgr.InputRelays.GetRelayStatus(inputURL)
	assert.Equal(t, 1, refCount, "RefCount should be 1 (HLS only)")
	assert.Equal(t, stream.InputRunning, status, "Input should still be running")

	// Stop 2 HLS viewers (HLS session should remain because 1 viewer still active)
	t.Log("Step 6: Stop 2 of 3 HLS viewers (session remains)")
	execute(2, concurrent, func(i int) {
		resp, err := doRequest("POST", "/api/relay/hls/stop-viewer", map[string]interface{}{
			"input_name": inputName,
			"viewer_id":  viewerIDs[i],
		})
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	})

	time.Sleep(500 * time.Millisecond)
	status, refCount, _ = relayMgr.InputRelays.GetRelayStatus(inputURL)
	assert.Equal(t, 1, refCount, "RefCount still 1 (HLS has 1 viewer left)")
	assert.Equal(t, stream.InputRunning, status, "Input should still be running")

	// Stop final HLS viewer and trigger cleanup
	t.Log("Step 7: Stop final HLS viewer → cleanup → RefCount 0")
	resp, err = doRequest("POST", "/api/relay/hls/stop-viewer", map[string]interface{}{
		"input_name": inputName,
		"viewer_id":  viewerIDs[2],
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Manually trigger HLS session cleanup (simulating sessionTimeout cleanup)
	hlsMgr.DeleteSession(inputName)

	// Allow time for cleanup to complete
	time.Sleep(500 * time.Millisecond)

	// Verify Input Relay has been stopped
	status, refCount, _ = relayMgr.InputRelays.GetRelayStatus(inputURL)
	assert.Equal(t, 0, refCount, "RefCount should be 0 after all consumers stop")
	assert.Equal(t, stream.InputStopped, status, "Input relay should be stopped")

	t.Log("=== SUCCESS: RefCount 7→2→1→0, Input relay stopped correctly ===")
}

// TestFullStack_MultiConsumerLifecycle tests sequential consumer lifecycle
func TestFullStack_MultiConsumerLifecycle(t *testing.T) {
	runFullStackLifecycle(t, false)
}

// TestFullStack_ConcurrentConsumers tests concurrent consumer lifecycle
func TestFullStack_ConcurrentConsumers(t *testing.T) {
	runFullStackLifecycle(t, true)
}
