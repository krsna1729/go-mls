package stream

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go-mls/internal/logger"
)

// ---- TEST HELPERS ----

// TestRecordingManager_ConcurrentAPI exercises the public API concurrently to ensure thread safety.
func TestRecordingManager_ConcurrentAPI(t *testing.T) {
	log := logger.NewLogger()
	dir := t.TempDir()
	relayMgr := NewRelayManager(log, dir, "")
	rm := NewRecordingManager(log, dir, relayMgr)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	num := 10
	var wg sync.WaitGroup
	recNames := make([]string, num)
	for i := 0; i < num; i++ {
		recNames[i] = "rec" + string(rune('A'+i))
	}

	source := "testsrc"

	// Start recordings concurrently
	for i := 0; i < num; i++ {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_ = rm.StartRecording(ctx, name, source) // Use a dummy source
		}(recNames[i])
	}

	// Stop recordings concurrently
	for i := 0; i < num; i++ {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_ = rm.StopRecording(name, source)
		}(recNames[i])
	}

	// List recordings concurrently
	for i := 0; i < num; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = rm.ListRecordings()
		}()
	}

	wg.Wait()
}

func TestRecordingManager_DeleteRecordingByFilename(t *testing.T) {
	dir := t.TempDir()
	filename := "testfile.mp4"
	filePath := filepath.Join(dir, filename)
	content := []byte("dummy recording data")
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	log := logger.NewLogger()
	rm := NewRecordingManager(log, dir, nil)
	// Add a recording to the manager
	rm.recordings["testkey"] = &Recording{
		Name:     "test",
		Source:   "source",
		Filename: filename,
		FilePath: filePath,
		Active:   false,
	}

	// Test valid delete
	err := rm.DeleteRecordingByFilename(filename)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("expected file to be deleted, got err=%v", err)
	}
	if _, ok := rm.recordings["testkey"]; ok {
		t.Errorf("expected recording to be removed from map")
	}

	// Test deleting non-existent file
	err = rm.DeleteRecordingByFilename("notfound.mp4")
	if err == nil {
		t.Errorf("expected error for missing file, got nil")
	}
}

func TestRecordingManager_lastUnderscore(t *testing.T) {
	if lastUnderscore("") != -1 {
		t.Errorf("expected -1 for empty string")
	}
	if lastUnderscore("foo_bar_baz") != 7 {
		t.Errorf("expected 7 for 'foo_bar_baz'")
	}
	if lastUnderscore("nounderscore") != -1 {
		t.Errorf("expected -1 for 'nounderscore'")
	}
}

func TestSSEBroker_AddRemoveClient(t *testing.T) {
	ch := make(chan string, 1)
	sseBroker.AddClient(ch)
	sseBroker.NotifyAll("test event")
	select {
	case msg := <-ch:
		if msg != "test event" {
			t.Errorf("expected 'test event', got %q", msg)
		}
	default:
		t.Errorf("expected to receive notification")
	}
	sseBroker.RemoveClient(ch)
	sseBroker.NotifyAll("another event")
	select {
	case <-ch:
		t.Errorf("should not receive after removal")
	default:
		// ok
	}
}

func TestRecordingManager_ListRecordings_Empty(t *testing.T) {
	log := logger.NewLogger()
	dir := t.TempDir()
	relayMgr := NewRelayManager(log, dir, "")
	rm := NewRecordingManager(log, dir, relayMgr)
	list := rm.ListRecordings()
	if len(list) != 0 {
		t.Errorf("expected empty list, got %v", list)
	}
}

func TestApiRecordingsSSE(t *testing.T) {
	ts := httptest.NewServer(ApiRecordingsSSE())
	defer ts.Close()

	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	// Read a small amount of data to ensure the connection is open, then exit
	buf := make([]byte, 128)
	_, _ = resp.Body.Read(buf)
}

func TestRecordingManager_ListRecordings_DiskAndMemory(t *testing.T) {
	dir := t.TempDir()
	log := logger.NewLogger()
	rm := NewRecordingManager(log, dir, nil)

	// Create a file on disk only
	diskFile := "diskonly_1234.mp4"
	diskFilePath := filepath.Join(dir, diskFile)
	if err := os.WriteFile(diskFilePath, []byte("data"), 0644); err != nil {
		t.Fatalf("failed to create disk file: %v", err)
	}

	// Create a recording in memory only
	memFile := "memonly_5678.mp4"
	memRec := &Recording{
		Name:     "memonly",
		Source:   "src",
		Filename: memFile,
		FilePath: filepath.Join(dir, memFile),
		Active:   false,
	}
	rm.recordings["memkey"] = memRec

	// Create a recording in both memory and disk
	bothFile := "both_9999.mp4"
	bothFilePath := filepath.Join(dir, bothFile)
	if err := os.WriteFile(bothFilePath, []byte("data2"), 0644); err != nil {
		t.Fatalf("failed to create both file: %v", err)
	}
	bothRec := &Recording{
		Name:     "both",
		Source:   "src",
		Filename: bothFile,
		FilePath: bothFilePath,
		Active:   false,
	}
	rm.recordings["bothkey"] = bothRec

	list := rm.ListRecordings()
	var foundDisk, foundMem, foundBoth bool
	for _, r := range list {
		if r.Filename == diskFile {
			foundDisk = true
			if r.FileSize == 0 {
				t.Errorf("expected disk file size to be set")
			}
		}
		if r.Filename == memFile {
			foundMem = true
		}
		if r.Filename == bothFile {
			foundBoth = true
		}
	}
	if !foundDisk {
		t.Errorf("disk-only file not found in list")
	}
	if !foundMem {
		t.Errorf("mem-only file not found in list")
	}
	if !foundBoth {
		t.Errorf("both file not found in list")
	}
}

func TestRecordingManager_StartRecording_Duplicate(t *testing.T) {
	dir := t.TempDir()
	log := logger.NewLogger()
	rm := NewRecordingManager(log, dir, nil)
	ctx := context.Background()
	name := "recdup"
	source := "srcdup"
	// Add an active recording with the same name and source
	rm.recordings["dupkey"] = &Recording{
		Name:   name,
		Source: source,
		Active: true,
	}
	// Should return error for duplicate
	err := rm.StartRecording(ctx, name, source)
	if err == nil || err.Error() != "active recording for name recdup and source srcdup already exists" {
		t.Errorf("expected duplicate error, got %v", err)
	}
}

func TestRecordingManager_StartRecording_ErrorBranches(t *testing.T) {
	dir := t.TempDir()
	log := logger.NewLogger()
	ctx := context.Background()
	// Use a real RelayManager
	relayMgr := NewRelayManager(log, dir, "")
	rm := NewRecordingManager(log, dir, relayMgr)
	// Try to start a recording with a non-existent source (should fail at ffmpeg step)
	err := rm.StartRecording(ctx, "fail", "fail")
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}

func TestRecordingManager_StartRecording_Success(t *testing.T) {
	t.Parallel()
	log := logger.NewLogger()
	dir := t.TempDir()

	// Start RTSP server on dynamic port
	rtspServer := NewRTSPServerManager(log, "127.0.0.1", 0)
	if err := rtspServer.Start(); err != nil {
		t.Fatalf("failed to start RTSP server: %v", err)
	}
	defer rtspServer.Stop()

	relayMgr := NewRelayManager(log, dir, "")
	relayMgr.SetRTSPServer(rtspServer)

	// Copy testsrc.mp4 to temp dir and chdir
	testSrcPath := filepath.Join("..", "..", "testdata", "testsrc.mp4")
	testDestPath := filepath.Join(dir, "testsrc.mp4")
	srcFile, err := os.Open(testSrcPath)
	if err != nil {
		t.Fatalf("failed to open testsrc.mp4: %v", err)
	}
	defer srcFile.Close()
	destFile, err := os.Create(testDestPath)
	if err != nil {
		t.Fatalf("failed to create dest testsrc.mp4: %v", err)
	}
	defer destFile.Close()
	_, _ = io.Copy(destFile, srcFile)

	// Change working directory to temp dir so file://testsrc.mp4 resolves
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	defer os.Chdir(oldwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	// Register input config for testsrc (relative path)
	relayMgr.RegisterInputConfig("testrec", "file://testsrc.mp4")
	if _, err := relayMgr.StartInputRelayForConsumer("testrec"); err != nil {
		t.Fatalf("failed to start input relay for consumer: %v", err)
	}

	rm := NewRecordingManager(log, dir, relayMgr)
	defer rm.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	name := "testrec"
	source := "testrec"

	err = rm.StartRecording(ctx, name, source)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Check that the recording is present and active
	found := false
	for _, rec := range rm.ListRecordings() {
		if rec.Name == name && rec.Source == source && rec.Active {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("recording not found or not active after StartRecording")
	}

	// Cleanup: stop the recording
	err = rm.StopRecording(name, source)
	if err != nil {
		t.Errorf("failed to stop recording: %v", err)
	}
}
