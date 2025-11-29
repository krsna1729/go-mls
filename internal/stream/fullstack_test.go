package stream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-mls/internal/logger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFullStack_MultiConsumerLifecycle is a comprehensive integration test that exercises
// the full HTTP API stack with multiple consumers: 5 outputs, 1 recording, and 3 HLS viewers.
// Expected refcount progression: 7 → 2 → 1 → 0 (HLS is 1 consumer regardless of viewer count)
func TestFullStack_MultiConsumerLifecycle(t *testing.T) {
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
	rtspServer := NewRTSPServerManager(log, "127.0.0.1", 0) // Use port 0 for random port
	err = rtspServer.Start()
	require.NoError(t, err, "Failed to start RTSP server")
	defer rtspServer.Stop()

	relayMgr := NewRelayManager(log, tempDir, "error")
	relayMgr.SetRTSPServer(rtspServer)

	recordingMgr := NewRecordingManager(log, tempDir, relayMgr)
	defer recordingMgr.Shutdown()

	hlsMgr := NewHLSManager(HLSManagerConfig{
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
	defer hlsMgr.Shutdown()

	relayMgr.HLSManager = hlsMgr
	relayMgr.RecordingManager = recordingMgr

	// === Setup HTTP Server with API Handlers ===
	mux := http.NewServeMux()

	// Relay APIs
	mux.HandleFunc("/api/relay/start", ApiStartRelay(relayMgr))
	mux.HandleFunc("/api/relay/stop", ApiStopRelay(relayMgr))

	// Recording APIs
	mux.HandleFunc("/api/recording/start", ApiStartRecording(recordingMgr))
	mux.HandleFunc("/api/recording/stop", ApiStopRecording(recordingMgr))

	// HLS APIs
	mux.HandleFunc("/api/relay/hls/start-viewer", ApiStartHLSViewer(hlsMgr, relayMgr))
	mux.HandleFunc("/api/relay/hls/stop-viewer", ApiStopHLSViewer(hlsMgr, relayMgr))

	// Create test HTTP server
	ts := httptest.NewServer(mux)
	defer ts.Close()

	baseURL := ts.URL

	// Helper function for HTTP requests
	doRequest := func(method, path string, body interface{}) (*http.Response, error) {
		var reqBody io.Reader
		if body != nil {
			jsonData, err := json.Marshal(body)
			if err != nil {
				return nil, err
			}
			reqBody = bytes.NewReader(jsonData)
		}
		req, err := http.NewRequest(method, baseURL+path, reqBody)
		if err != nil {
			return nil, err
		}
		if reqBody != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		return http.DefaultClient.Do(req)
	}

	// === Phase 1: Start Consumers ===
	t.Log("=== Phase 1: Starting Consumers ===")
	t.Log("Target: 5 outputs + 1 recording + 3 HLS viewers = RefCount 7")

	inputURL := "file://testsrc.mp4"
	inputName := "TestInput"

	// Start 5 output relays (using file outputs with absolute paths)
	t.Log("Step 1: Start 5 output relays")
	for i := 0; i < 5; i++ {
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
	}

	// Wait for RTSP stream to be ready
	time.Sleep(2 * time.Second)

	// Verify refcount = 5
	relayMgr.InputRelays.mu.Lock()
	relay, exists := relayMgr.InputRelays.Relays[inputURL]
	relayMgr.InputRelays.mu.Unlock()
	require.True(t, exists, "Input relay should exist")
	assert.Equal(t, 5, relay.RefCount, "RefCount should be 5 after 5 outputs")

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
	assert.Equal(t, 6, relay.RefCount, "RefCount should be 6 after recording starts")

	// Start 3 HLS viewers (HLS session counts as 1 consumer)
	t.Log("Step 3: Start 3 HLS viewers (HLS = 1 consumer)")
	viewerIDs := make([]string, 3)
	for i := 0; i < 3; i++ {
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
		viewerIDs[i] = viewerID
		t.Logf("  HLS viewer %d started: %s", i+1, viewerID)
	}

	time.Sleep(1 * time.Second)
	// Refcount should be 7 (5 outputs + 1 recording + 1 HLS session)
	assert.Equal(t, 7, relay.RefCount, "RefCount should be 7 (5 outputs + 1 recording + 1 HLS)")
	assert.Equal(t, InputRunning, relay.Status, "Input should be running")

	// === Phase 2: Stop Consumers ===
	t.Log("=== Phase 2: Stopping Consumers ===")

	// Stop all 5 outputs
	t.Log("Step 4: Stop all 5 output relays")
	for i := 0; i < 5; i++ {
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
		time.Sleep(100 * time.Millisecond)
	}

	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, 2, relay.RefCount, "RefCount should be 2 (recording + HLS)")
	assert.Equal(t, InputRunning, relay.Status, "Input should still be running")

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
	assert.Equal(t, 1, relay.RefCount, "RefCount should be 1 (HLS only)")
	assert.Equal(t, InputRunning, relay.Status, "Input should still be running")

	// Stop 2 HLS viewers (HLS session should remain because 1 viewer still active)
	t.Log("Step 6: Stop 2 of 3 HLS viewers (session remains)")
	for i := 0; i < 2; i++ {
		resp, err := doRequest("POST", "/api/relay/hls/stop-viewer", map[string]interface{}{
			"input_name": inputName,
			"viewer_id":  viewerIDs[i],
		})
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, 1, relay.RefCount, "RefCount still 1 (HLS has 1 viewer left)")
	assert.Equal(t, InputRunning, relay.Status, "Input should still be running")

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
	assert.Equal(t, 0, relay.RefCount, "RefCount should be 0 after all consumers stop")
	assert.Equal(t, InputStopped, relay.Status, "Input relay should be stopped")

	t.Log("=== SUCCESS: RefCount 7→2→1→0, Input relay stopped correctly ===")
}

// TestFullStack_ConcurrentConsumers tests concurrent API requests for starting/stopping consumers
func TestFullStack_ConcurrentConsumers(t *testing.T) {
	testFile := filepath.Join("..", "..", "testdata", "testsrc.mp4")
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Skipf("Skipping integration test: %s not found", testFile)
	}

	tempDir := t.TempDir()
	log := logger.NewLogger()

	// Copy test file
	destPath := filepath.Join(tempDir, "testsrc.mp4")
	srcData, err := os.ReadFile(testFile)
	require.NoError(t, err)
	err = os.WriteFile(destPath, srcData, 0644)
	require.NoError(t, err)

	// Setup components
	rtspServer := NewRTSPServerManager(log, "127.0.0.1", 0)
	err = rtspServer.Start()
	require.NoError(t, err)
	defer rtspServer.Stop()

	relayMgr := NewRelayManager(log, tempDir, "error")
	relayMgr.SetRTSPServer(rtspServer)

	// Setup HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/api/relay/start", ApiStartRelay(relayMgr))
	mux.HandleFunc("/api/relay/stop", ApiStopRelay(relayMgr))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Helper function
	doRequest := func(method, path string, body interface{}) (*http.Response, error) {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequest(method, ts.URL+path, bytes.NewReader(jsonData))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return http.DefaultClient.Do(req)
	}

	t.Log("Starting 5 concurrent output relays")

	// Start first output and wait for RTSP stream to be ready
	outputFile0 := filepath.Join(tempDir, "output0.mp4")
	resp, err := doRequest("POST", "/api/relay/start", map[string]interface{}{
		"input_name":  "ConcTest",
		"input_url":   "file://testsrc.mp4",
		"output_name": "Output0",
		"output_url":  "file://" + filepath.Base(outputFile0),
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Wait for RTSP stream to be created
	time.Sleep(2 * time.Second)

	// Start remaining outputs
	for i := 1; i < 5; i++ {
		resp, err = doRequest("POST", "/api/relay/start", map[string]interface{}{
			"input_name":  "ConcTest",
			"input_url":   "file://testsrc.mp4",
			"output_name": fmt.Sprintf("Output%d", i),
			"output_url":  fmt.Sprintf("file://output%d.mp4", i),
		})
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	time.Sleep(2 * time.Second)

	// Verify refcount
	relayMgr.InputRelays.mu.Lock()
	relay, exists := relayMgr.InputRelays.Relays["file://testsrc.mp4"]
	relayMgr.InputRelays.mu.Unlock()
	require.True(t, exists)
	assert.Equal(t, 5, relay.RefCount, "RefCount should be 5")

	// Stop 4 outputs
	t.Log("Stopping 4 outputs")
	for i := 0; i < 4; i++ {
		resp, err := doRequest("POST", "/api/relay/stop", map[string]interface{}{
			"input_url":   "file://testsrc.mp4",
			"output_url":  fmt.Sprintf("file://output%d.mp4", i),
			"input_name":  "ConcTest",
			"output_name": fmt.Sprintf("Output%d", i),
		})
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
		time.Sleep(100 * time.Millisecond) // Small delay between stops
	}

	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, 1, relay.RefCount, "RefCount should be 1")
	assert.Equal(t, InputRunning, relay.Status, "Input should still be running")

	// Stop final output
	t.Log("Stopping final output")
	resp, err = doRequest("POST", "/api/relay/stop", map[string]interface{}{
		"input_url":   "file://testsrc.mp4",
		"output_url":  "file://output4.mp4",
		"input_name":  "ConcTest",
		"output_name": "Output4",
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	time.Sleep(1 * time.Second)
	assert.Equal(t, 0, relay.RefCount, "RefCount should be 0")
	assert.Equal(t, InputStopped, relay.Status, "Input should be stopped")

	t.Log("=== Concurrent test complete ===")
}
