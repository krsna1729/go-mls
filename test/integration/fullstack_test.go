package integration_test

import (
	"bytes"
	"context"
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

	"go-mls/internal/api"
	"go-mls/internal/app"
	"go-mls/internal/config"
	"go-mls/internal/logger"

	"github.com/stretchr/testify/require"
)

type testEnv struct {
	tempDir   string
	appCtx    *app.Context
	server    *api.Server
	ts        *httptest.Server
	cfg       *config.Config
	hubType   string
	streamURL string
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

func (e *testEnv) shutdown() {
	if e.server != nil {
		e.server.Shutdown()
	}
	if e.appCtx != nil {
		e.appCtx.Shutdown()
	}
}

func checkTestFile(t *testing.T) {
	testFile := filepath.Join("..", "..", "testdata", "testsrc.mp4")
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Skipf("Skipping integration test: %s not found", testFile)
	}
}

func setupTestEnv(t *testing.T, hubType string) *testEnv {
	checkTestFile(t)

	tempDir := t.TempDir()
	log := logger.NewLogger()

	cfg := &config.Config{
		HTTP: config.HTTPConfig{
			Host:         "127.0.0.1",
			Port:         "0",
			ReadTimeout:  config.Duration(30 * time.Second),
			WriteTimeout: config.Duration(30 * time.Second),
			IdleTimeout:  config.Duration(120 * time.Second),
		},
		Relay: config.RelayConfig{
			InputTimeout:  config.Duration(30 * time.Second),
			OutputTimeout: config.Duration(60 * time.Second),
			RTMPHub: config.RTMPConfig{
				Host: "127.0.0.1",
				Port: 1935,
			},
		},
		Recording: config.RecordingConfig{
			Directory: tempDir,
		},
		Logging: config.LoggingConfig{
			Level: "warn",
		},
		HLS: config.HLSConfig{
			PlaylistBaseDir: tempDir,
			FFmpegPreset:    "ultrafast",
		},
		FFmpeg: config.FFmpegConfig{
			Path: "ffmpeg",
		},
	}

	var rtmpPort int
	var rtspAddr string

	switch hubType {
	case "rtsp":
		cfg.Relay.HubType = "rtsp"
		cfg.Relay.RTSPServer = config.RTSPConfig{
			Host: "127.0.0.1",
			Port: 0,
		}
		rtmpPort = 0
	default:
		hubType = "rtmp"
		rtmpPort = 1935
	}

	appCtx, err := app.NewContext(cfg, log)
	if err != nil {
		t.Fatalf("Failed to create app context: %v", err)
	}

	if err := appCtx.Start(); err != nil {
		t.Fatalf("Failed to start app context: %v", err)
	}

	hubAddr := appCtx.Hub.Addr()
	t.Logf("%s Hub listening at: %s", hubType, hubAddr)

	server := api.NewServer(
		appCtx.Store,
		appCtx.Ingest,
		appCtx.HLSMgr,
		log,
		tempDir,
		tempDir,
		rtmpPort,
		rtspAddr,
		context.Background(),
	)

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)

	return &testEnv{
		tempDir:   tempDir,
		appCtx:    appCtx,
		server:    server,
		ts:        ts,
		cfg:       cfg,
		hubType:   hubType,
		streamURL: hubAddr,
	}
}

func TestInputsAPI(t *testing.T) {
	for _, hubType := range []string{"rtmp", "rtsp"} {
		t.Run(hubType, func(t *testing.T) {
			env := setupTestEnv(t, hubType)
			defer env.shutdown()

			resp, err := env.doRequest("POST", "/inputs", map[string]interface{}{
				"stream_path": "test-stream",
			})
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()

			resp, err = env.doRequest("GET", "/inputs", nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var inputs []map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&inputs)
			resp.Body.Close()
			require.NoError(t, err)
			require.Len(t, inputs, 1)

			resp, err = env.doRequest("DELETE", "/inputs?stream=test-stream", nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()
		})
	}
}

func TestOutputsAPI(t *testing.T) {
	for _, hubType := range []string{"rtmp", "rtsp"} {
		t.Run(hubType, func(t *testing.T) {
			env := setupTestEnv(t, hubType)
			defer env.shutdown()

			resp, err := env.doRequest("POST", "/inputs", map[string]interface{}{
				"stream_path": "test-stream",
			})
			require.NoError(t, err)
			resp.Body.Close()

			remoteURL := "rtmp://youtube.com/live/stream-key"
			if hubType == "rtsp" {
				remoteURL = "rtsp://127.0.0.1:8554/stream"
			}

			resp, err = env.doRequest("POST", "/outputs", map[string]interface{}{
				"stream_path": "test-stream",
				"output_id":   "test-output",
				"remote_url":  remoteURL,
			})
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()

			resp, err = env.doRequest("GET", "/outputs?stream=test-stream", nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()

			resp, err = env.doRequest("DELETE", "/outputs?stream=test-stream&id=test-output", nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()

			resp, err = env.doRequest("DELETE", "/inputs?stream=test-stream", nil)
			require.NoError(t, err)
			resp.Body.Close()
		})
	}
}

func TestRecordingAPI(t *testing.T) {
	for _, hubType := range []string{"rtmp", "rtsp"} {
		t.Run(hubType, func(t *testing.T) {
			env := setupTestEnv(t, hubType)
			defer env.shutdown()

			resp, err := env.doRequest("POST", "/inputs", map[string]interface{}{
				"stream_path": "test-stream",
			})
			require.NoError(t, err)
			resp.Body.Close()

			resp, err = env.doRequest("POST", "/record?stream=test-stream", nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()

			resp, err = env.doRequest("DELETE", "/record?stream=test-stream", nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()

			resp, err = env.doRequest("DELETE", "/inputs?stream=test-stream", nil)
			require.NoError(t, err)
			resp.Body.Close()
		})
	}
}

func TestHLSAPI(t *testing.T) {
	for _, hubType := range []string{"rtmp", "rtsp"} {
		t.Run(hubType, func(t *testing.T) {
			env := setupTestEnv(t, hubType)
			defer env.shutdown()

			resp, err := env.doRequest("POST", "/inputs", map[string]interface{}{
				"stream_path": "test-stream",
			})
			require.NoError(t, err)
			resp.Body.Close()

			resp, err = env.doRequest("POST", "/hls/start?stream=test-stream", nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var hlsResp map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&hlsResp)
			resp.Body.Close()
			require.NoError(t, err)

			viewerID := hlsResp["viewer_id"].(string)

			resp, err = env.doRequest("POST", "/hls/stop", map[string]interface{}{
				"stream":    "test-stream",
				"viewer_id": viewerID,
			})
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()

			resp, err = env.doRequest("DELETE", "/inputs?stream=test-stream", nil)
			require.NoError(t, err)
			resp.Body.Close()
		})
	}
}

func TestHLSHeartbeatAPI(t *testing.T) {
	for _, hubType := range []string{"rtmp", "rtsp"} {
		t.Run(hubType, func(t *testing.T) {
			env := setupTestEnv(t, hubType)
			defer env.shutdown()

			resp, err := env.doRequest("POST", "/inputs", map[string]interface{}{
				"stream_path": "test-stream",
			})
			require.NoError(t, err)
			resp.Body.Close()

			resp, err = env.doRequest("POST", "/hls/start?stream=test-stream", nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var hlsResp map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&hlsResp)
			resp.Body.Close()
			require.NoError(t, err)

			viewerID := hlsResp["viewer_id"].(string)

			resp, err = env.doRequest("POST", "/hls/heartbeat", map[string]interface{}{
				"stream":    "test-stream",
				"viewer_id": viewerID,
			})
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()

			resp, err = env.doRequest("POST", "/hls/stop", map[string]interface{}{
				"stream":    "test-stream",
				"viewer_id": viewerID,
			})
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()

			resp, err = env.doRequest("POST", "/hls/heartbeat", map[string]interface{}{
				"stream":    "test-stream",
				"viewer_id": viewerID,
			})
			require.NoError(t, err)
			require.Equal(t, http.StatusGone, resp.StatusCode)
			resp.Body.Close()
		})
	}
}

func TestStatsAPI(t *testing.T) {
	for _, hubType := range []string{"rtmp", "rtsp"} {
		t.Run(hubType, func(t *testing.T) {
			env := setupTestEnv(t, hubType)
			defer env.shutdown()

			resp, err := env.doRequest("GET", "/stats", nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var stats map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&stats)
			resp.Body.Close()
			require.NoError(t, err)
			require.NotNil(t, stats["server"])
			require.NotNil(t, stats["inputs"])
			require.NotNil(t, stats["outputs"])
		})
	}
}

func TestFullStackLifecycle(t *testing.T) {
	for _, hubType := range []string{"rtmp", "rtsp"} {
		t.Run(hubType, func(t *testing.T) {
			env := setupTestEnv(t, hubType)
			defer env.shutdown()
			streamPath := "test-stream"

			t.Log("Creating input")
			resp, err := env.doRequest("POST", "/inputs", map[string]interface{}{
				"stream_path": streamPath,
			})
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()

			t.Log("Creating outputs")
			for i := 0; i < 3; i++ {
				remoteURL := fmt.Sprintf("file://%s/out%d.flv", env.tempDir, i)
				resp, err = env.doRequest("POST", "/outputs", map[string]interface{}{
					"stream_path": streamPath,
					"output_id":   fmt.Sprintf("out%d", i),
					"remote_url":  remoteURL,
				})
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, resp.StatusCode)
				resp.Body.Close()
			}

			time.Sleep(500 * time.Millisecond)

			t.Log("Starting recording")
			resp, err = env.doRequest("POST", "/record?stream="+streamPath, nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()

			t.Log("Starting HLS viewer")
			resp, err = env.doRequest("POST", "/hls/start?stream="+streamPath, nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()

			t.Log("Verifying stats")
			resp, err = env.doRequest("GET", "/stats", nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var stats map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&stats)
			resp.Body.Close()
			require.NoError(t, err)
			require.NotEmpty(t, stats["inputs"])
			require.NotEmpty(t, stats["outputs"])

			t.Log("Stopping HLS")
			resp, err = env.doRequest("POST", "/hls/stop", map[string]interface{}{
				"stream":    streamPath,
				"viewer_id": "auto",
			})
			require.NoError(t, err)
			resp.Body.Close()

			t.Log("Stopping recording")
			resp, err = env.doRequest("DELETE", "/record?stream="+streamPath, nil)
			require.NoError(t, err)
			resp.Body.Close()

			t.Log("Stopping outputs")
			for i := 0; i < 3; i++ {
				resp, err = env.doRequest("DELETE", fmt.Sprintf("/outputs?stream=%s&id=out%d", streamPath, i), nil)
				require.NoError(t, err)
				resp.Body.Close()
			}

			t.Log("Deleting input")
			resp, err = env.doRequest("DELETE", "/inputs?stream="+streamPath, nil)
			require.NoError(t, err)
			resp.Body.Close()

			t.Log("=== SUCCESS: Full lifecycle completed ===")
		})
	}
}

func TestConcurrentOutputs(t *testing.T) {
	for _, hubType := range []string{"rtmp", "rtsp"} {
		t.Run(hubType, func(t *testing.T) {
			env := setupTestEnv(t, hubType)
			defer env.shutdown()
			streamPath := "test-stream"

			resp, err := env.doRequest("POST", "/inputs", map[string]interface{}{
				"stream_path": streamPath,
			})
			require.NoError(t, err)
			resp.Body.Close()

			var wg sync.WaitGroup
			n := 5
			wg.Add(n)

			for i := 0; i < n; i++ {
				go func(idx int) {
					defer wg.Done()
					resp, err := env.doRequest("POST", "/outputs", map[string]interface{}{
						"stream_path": streamPath,
						"output_id":   fmt.Sprintf("concurrent_%d", idx),
						"remote_url":  fmt.Sprintf("file://%s/out%d.flv", env.tempDir, idx),
					})
					require.NoError(t, err)
					require.Equal(t, http.StatusOK, resp.StatusCode)
					resp.Body.Close()
				}(i)
			}

			wg.Wait()

			resp, err = env.doRequest("GET", "/outputs?stream="+streamPath, nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var outputs []map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&outputs)
			resp.Body.Close()
			require.NoError(t, err)
			require.Len(t, outputs, n)

			for i := 0; i < n; i++ {
				resp, err = env.doRequest("DELETE", fmt.Sprintf("/outputs?stream=%s&id=concurrent_%d", streamPath, i), nil)
				require.NoError(t, err)
				resp.Body.Close()
			}

			resp, err = env.doRequest("DELETE", "/inputs?stream="+streamPath, nil)
			require.NoError(t, err)
			resp.Body.Close()
		})
	}
}

func TestOutputStartStopRoutes(t *testing.T) {
	for _, hubType := range []string{"rtmp", "rtsp"} {
		t.Run(hubType, func(t *testing.T) {
			env := setupTestEnv(t, hubType)
			defer env.shutdown()

			resp, err := env.doRequest("POST", "/inputs", map[string]interface{}{
				"stream_path": "test-stream",
			})
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()

			resp, err = env.doRequest("POST", "/outputs", map[string]interface{}{
				"stream_path": "test-stream",
				"output_id":   "out1",
				"remote_url":  fmt.Sprintf("file://%s/out1.flv", env.tempDir),
			})
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()

			resp, err = env.doRequest("POST", "/outputs/stop", map[string]interface{}{
				"stream_path": "test-stream",
				"output_id":   "out1",
			})
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()

			resp, err = env.doRequest("POST", "/outputs/start", map[string]interface{}{
				"stream_path": "test-stream",
				"output_id":   "out1",
			})
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()
		})
	}
}
