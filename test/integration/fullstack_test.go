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

	"github.com/stretchr/testify/require"
)

type testEnv struct {
	tempDir  string
	pipeline *stream.Pipeline
	ts       *httptest.Server
}

func (e *testEnv) doRequest(method, path string, body interface{}) (*http.Response, error) {
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

func setupTestEnv(t *testing.T) *testEnv {
	testFile := filepath.Join("..", "..", "testdata", "testsrc.mp4")
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Skipf("Skipping integration test: %s not found", testFile)
	}

	tempDir := t.TempDir()
	log := logger.NewLogger()

	destPath := filepath.Join(tempDir, "testsrc.mp4")
	srcData, err := os.ReadFile(testFile)
	require.NoError(t, err, "Failed to read test file")
	err = os.WriteFile(destPath, srcData, 0644)
	require.NoError(t, err, "Failed to copy test file to temp dir")

	rtspServer := stream.NewRTSPServerManager(log, "127.0.0.1", 0)
	err = rtspServer.Start()
	require.NoError(t, err, "Failed to start RTSP server")
	t.Cleanup(func() { rtspServer.Stop() })

	pipeline := stream.NewPipeline(log, tempDir, 60*time.Second)
	pipeline.SetRTSPServer(rtspServer)
	t.Cleanup(func() { pipeline.Shutdown() })

	mux := http.NewServeMux()
	mux.HandleFunc("/api/relay/start", stream.ApiStartOutputRelay(pipeline))
	mux.HandleFunc("/api/relay/stop", stream.ApiStopOutputRelay(pipeline))
	mux.HandleFunc("/api/relay/status", stream.ApiRelayStatus(pipeline))
	mux.HandleFunc("/api/recording/start", stream.ApiStartRecording(pipeline))
	mux.HandleFunc("/api/recording/stop", stream.ApiStopRecording(pipeline))
	mux.HandleFunc("/api/relay/hls/start-viewer", stream.ApiStartHLSViewer(pipeline))
	mux.HandleFunc("/api/relay/hls/stop-viewer", stream.ApiStopHLSViewer(pipeline))
	mux.HandleFunc("/api/relay/delete-input", stream.ApiDeleteInput(pipeline))

	ts := httptest.NewServer(mux)
	t.Cleanup(func() { ts.Close() })

	return &testEnv{
		tempDir:  tempDir,
		pipeline: pipeline,
		ts:       ts,
	}
}

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

func runFullStackLifecycle(t *testing.T, concurrent bool) {
	env := setupTestEnv(t)
	pipeline := env.pipeline
	doRequest := env.doRequest

	t.Logf("=== Testing Full Stack Lifecycle (Concurrent: %v) ===", concurrent)

	inputURL := "file://testsrc.mp4"
	inputName := "TestInput"

	t.Log("Phase 1: Starting consumers")
	_ = pipeline
	execute(5, concurrent, func(i int) {
		resp, err := doRequest("POST", "/api/relay/start", map[string]interface{}{
			"input_name":  inputName,
			"input_url":   inputURL,
			"output_name": fmt.Sprintf("Output%d", i),
			"output_url":  fmt.Sprintf("file://output%d.flv", i),
		})
		require.NoError(t, err, "Request should succeed")
		require.Equal(t, http.StatusOK, resp.StatusCode, "Output %d should start", i)
		resp.Body.Close()
	})

	t.Log("Step 2: Start recording")
	resp, err := doRequest("POST", "/api/recording/start", map[string]interface{}{
		"name":       "TestRec",
		"input_name": inputName,
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	time.Sleep(500 * time.Millisecond)

	t.Log("Step 3: Start HLS viewer")
	resp, err = doRequest("POST", "/api/relay/hls/start-viewer", map[string]interface{}{
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

	t.Log("Step 4: Verify status shows running streams")
	resp, err = doRequest("GET", "/api/relay/status", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var status stream.StatusResponse
	err = json.NewDecoder(resp.Body).Decode(&status)
	resp.Body.Close()
	require.NoError(t, err)
	require.NotEmpty(t, status.Relays, "Should have relays")
	require.NotEmpty(t, status.Relays[0].Outputs, "Should have outputs running")

	t.Log("Phase 2: Stopping consumers")

	t.Log("Step 5: Stop all outputs")
	execute(5, concurrent, func(i int) {
		resp, err := doRequest("POST", "/api/relay/stop", map[string]interface{}{
			"output_name": fmt.Sprintf("Output%d", i),
		})
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	})

	time.Sleep(500 * time.Millisecond)

	t.Log("Step 6: Stop recording")
	resp, err = doRequest("POST", "/api/recording/stop", map[string]interface{}{
		"name": "TestRec",
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	t.Log("Step 7: Stop HLS viewer")
	resp, err = doRequest("POST", "/api/relay/hls/stop-viewer", map[string]interface{}{
		"input_name": inputName,
		"viewer_id":  viewerID,
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	t.Log("Step 8: Delete input")
	resp, err = doRequest("POST", "/api/relay/delete-input", map[string]interface{}{
		"input_name": inputName,
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	time.Sleep(500 * time.Millisecond)

	t.Log("Step 9: Verify status shows clean state")
	resp, err = doRequest("GET", "/api/relay/status", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	err = json.NewDecoder(resp.Body).Decode(&status)
	resp.Body.Close()
	require.NoError(t, err)

	t.Log("=== SUCCESS: Full lifecycle completed ===")
	_ = pipeline
}

func TestFullStack_MultiConsumerLifecycle(t *testing.T) {
	runFullStackLifecycle(t, false)
}

func TestFullStack_ConcurrentConsumers(t *testing.T) {
	runFullStackLifecycle(t, true)
}

func TestRelayStartStop(t *testing.T) {
	env := setupTestEnv(t)
	defer env.pipeline.Shutdown()

	resp, err := env.doRequest("POST", "/api/relay/start", map[string]interface{}{
		"input_name":  "TestInput",
		"input_url":   "file://testsrc.mp4",
		"output_name": "TestOutput",
		"output_url":  "file://output.flv",
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp, err = env.doRequest("GET", "/api/relay/status", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp, err = env.doRequest("POST", "/api/relay/stop", map[string]interface{}{
		"output_name": "TestOutput",
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

func TestRecordingAPI(t *testing.T) {
	env := setupTestEnv(t)
	defer env.pipeline.Shutdown()

	resp, err := env.doRequest("POST", "/api/recording/start", map[string]interface{}{
		"name":       "TestRec",
		"input_name": "TestInput",
		"input_url":  "file://testsrc.mp4",
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp, err = env.doRequest("POST", "/api/recording/stop", map[string]interface{}{
		"name": "TestRec",
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

func TestHLSViewerAPI(t *testing.T) {
	env := setupTestEnv(t)
	defer env.pipeline.Shutdown()

	resp, err := env.doRequest("POST", "/api/relay/start", map[string]interface{}{
		"input_name":  "TestInput",
		"input_url":   "file://testsrc.mp4",
		"output_name": "TestOutput",
		"output_url":  "file://test.flv",
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp, err = env.doRequest("POST", "/api/relay/hls/start-viewer", map[string]interface{}{
		"input_name": "TestInput",
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var hlsResp map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&hlsResp)
	resp.Body.Close()
	require.NoError(t, err)

	viewerID := hlsResp["viewer_id"].(string)

	resp, err = env.doRequest("POST", "/api/relay/hls/stop-viewer", map[string]interface{}{
		"input_name": "TestInput",
		"viewer_id":  viewerID,
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}
