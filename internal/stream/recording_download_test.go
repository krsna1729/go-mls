package stream

import (
	"go-mls/internal/logger"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestApiDownloadRecording(t *testing.T) {
	dir := t.TempDir()
	filename := "testfile.mp4"
	filePath := filepath.Join(dir, filename)
	content := []byte("dummy recording data")
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	log := logger.NewLogger()
	rm := NewRecordingManager(log, dir, nil)
	ts := httptest.NewServer(ApiDownloadRecording(rm))
	defer ts.Close()

	// Test valid download
	resp, err := http.Get(ts.URL + "/download?filename=" + filename)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	respBody, _ := ioutil.ReadAll(resp.Body)
	if string(respBody) != string(content) {
		t.Errorf("expected file content, got %q", string(respBody))
	}
	resp.Body.Close()

	// Test file not found
	resp, err = http.Get(ts.URL + "/download?filename=notfound.mp4")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for missing file, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Test missing filename param
	resp, err = http.Get(ts.URL + "/download")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing filename, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestApiDownloadRecording_Errors(t *testing.T) {
	dir := t.TempDir()
	log := logger.NewLogger()
	rm := NewRecordingManager(log, dir, nil)
	ts := httptest.NewServer(ApiDownloadRecording(rm))
	defer ts.Close()

	// Create a test file and restrict permissions for access denied test
	testFile := "testfile.mp4"
	filePath := filepath.Join(dir, testFile)
	if err := os.WriteFile(filePath, []byte("dummy"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	// Remove all permissions
	if err := os.Chmod(filePath, 0000); err != nil {
		t.Fatalf("failed to chmod test file: %v", err)
	}
	defer os.Chmod(filePath, 0644) // Restore permissions for cleanup

	cases := []struct {
		name     string
		query    string
		wantCode int
		wantBody string
	}{
		{"missing filename", "", http.StatusBadRequest, "Missing filename"},
		{"path traversal ..", "/download?filename=..%2Fsecret.mp4", http.StatusBadRequest, "Invalid filename"},
		{"path traversal /", "/download?filename=foo/bar.mp4", http.StatusBadRequest, "Invalid filename"},
		{"path traversal \\", "/download?filename=foo%5Cbar.mp4", http.StatusBadRequest, "Invalid filename"},
		{"invalid extension", "/download?filename=video.txt", http.StatusBadRequest, "Invalid file type"},
		{"access denied", "/download?filename=testfile.mp4", http.StatusForbidden, "Access denied"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := ts.URL + tc.query
			resp, err := http.Get(url)
			if err != nil {
				t.Fatalf("GET failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantCode {
				t.Errorf("expected %d, got %d", tc.wantCode, resp.StatusCode)
			}
			body, _ := ioutil.ReadAll(resp.Body)
			if tc.wantBody != "" && !contains(string(body), tc.wantBody) {
				t.Errorf("expected body to contain %q, got %q", tc.wantBody, string(body))
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(substr) == 0 || (len(s) > 0 && (s == substr || (len(s) > len(substr) && (s[:len(substr)] == substr || contains(s[1:], substr)))))
}
