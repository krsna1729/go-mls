package integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-mls/internal/logger"
	"go-mls/internal/stream"

	"github.com/stretchr/testify/require"
)

// TestHTTPStartRelayConflict verifies HTTP API returns error when same input_url is used with different names
func TestHTTPStartRelayConflict(t *testing.T) {
	// Skip if test file doesn't exist
	testFile := filepath.Join("..", "..", "testdata", "testsrc.mp4")
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Skipf("Skipping test: %s not found", testFile)
	}

	// Setup
	tempDir := t.TempDir()
	log := logger.NewLogger()

	// Copy test file into tempDir
	data, err := os.ReadFile(testFile)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "testsrc.mp4"), data, 0644))

	// RTSP server
	rtspServer := stream.NewRTSPServerManager(log, "127.0.0.1", 0)
	require.NoError(t, rtspServer.Start())
	t.Cleanup(func() { rtspServer.Stop() })

	// Stream manager
	streamMgr := stream.NewStreamManager(log, tempDir, "error")
	streamMgr.SetRTSPServer(rtspServer)

	// HTTP mux
	mux := http.NewServeMux()
	mux.HandleFunc("/api/relay/start", stream.ApiStartOutputRelay(streamMgr))

	ts := httptest.NewServer(mux)
	defer ts.Close()

	doRequest := func(body interface{}) (*http.Response, []byte, error) {
		var b io.Reader
		if body != nil {
			jb, _ := json.Marshal(body)
			b = bytes.NewReader(jb)
		}
		req, err := http.NewRequest("POST", ts.URL+"/api/relay/start", b)
		if err != nil {
			return nil, nil, err
		}
		if b != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, err
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp, data, nil
	}

	inputURL := "file://testsrc.mp4"

	// First start with name A should succeed
	resp, body, err := doRequest(map[string]interface{}{
		"input_name":  "A",
		"input_url":   inputURL,
		"output_name": "outA",
		"output_url":  "file://outA.flv",
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))

	// Second start with same input_url but different name B should fail
	resp2, body2, err := doRequest(map[string]interface{}{
		"input_name":  "B",
		"input_url":   inputURL,
		"output_name": "outB",
		"output_url":  "file://outB.flv",
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusInternalServerError, resp2.StatusCode, string(body2))
}
