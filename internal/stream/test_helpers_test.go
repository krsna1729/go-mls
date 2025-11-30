package stream

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

var testSrcAbsolutePath string

// init finds the absolute path to testsrc.mp4 once when the package is loaded
func init() {
	// Start from current working directory and walk up to find project root
	wd, err := os.Getwd()
	if err != nil {
		panic("failed to get working directory: " + err.Error())
	}

	// Look for go.mod to identify project root, then find testdata
	dir := wd
	for {
		// Check if go.mod exists in current directory (project root indicator)
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			testSrcPath := filepath.Join(dir, "testdata", "testsrc.mp4")
			if _, err := os.Stat(testSrcPath); err == nil {
				testSrcAbsolutePath = testSrcPath
				return
			}
		}

		// Move up one directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding go.mod
			break
		}
		dir = parent
	}

	panic("failed to find testsrc.mp4 in project testdata directory")
}

// copyTestSrcToTempDir copies testsrc.mp4 to a temp dir and returns the dir and dest path
func copyTestSrcToTempDir(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	testDestPath := filepath.Join(dir, "testsrc.mp4")

	srcFile, err := os.Open(testSrcAbsolutePath)
	if err != nil {
		t.Fatalf("failed to open testsrc.mp4 from %s: %v", testSrcAbsolutePath, err)
	}
	defer srcFile.Close()

	destFile, err := os.Create(testDestPath)
	if err != nil {
		t.Fatalf("failed to create dest testsrc.mp4: %v", err)
	}
	defer destFile.Close()

	_, _ = io.Copy(destFile, srcFile)
	return dir, testDestPath
}

// chdirTo changes working directory to dir and restores it after the test
func chdirTo(t *testing.T, dir string) {
	t.Helper()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldwd)
	})
}
