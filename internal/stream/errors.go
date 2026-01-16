// Package stream contains errors for stream operations.
// All errors can be checked with errors.Is/errors.As for programmatic handling.
package stream

import (
	"errors"
	"fmt"
)

// Sentinel errors for common stream operations.
// Use errors.Is() to check for these specific error conditions.
var (
	// ErrRelayNotFound indicates the requested relay does not exist.
	ErrRelayNotFound = errors.New("relay not found")

	// ErrRelayNotRunning indicates the relay exists but is not currently running.
	ErrRelayNotRunning = errors.New("relay not running")

	// ErrRelayAlreadyExists indicates a relay with this identifier already exists.
	ErrRelayAlreadyExists = errors.New("relay already exists")

	// ErrFFmpegFailed indicates the FFmpeg process exited with an error.
	ErrFFmpegFailed = errors.New("ffmpeg process failed")

	// ErrFFmpegNotFound indicates FFmpeg binary was not found in PATH.
	ErrFFmpegNotFound = errors.New("ffmpeg not found")

	// ErrInvalidInput indicates the input URL is malformed or uses an unsupported scheme.
	ErrInvalidInput = errors.New("invalid input")

	// ErrInvalidOutput indicates the output URL is malformed or uses an unsupported scheme.
	ErrInvalidOutput = errors.New("invalid output")

	// ErrTimeout indicates an operation timed out.
	ErrTimeout = errors.New("operation timed out")

	// ErrShuttingDown indicates the system is shutting down and cannot accept new operations.
	ErrShuttingDown = errors.New("system is shutting down")

	// ErrRecordingNotFound indicates the requested recording does not exist.
	ErrRecordingNotFound = errors.New("recording not found")

	// ErrRecordingActive indicates an operation cannot be performed on an active recording.
	ErrRecordingActive = errors.New("recording is active")

	// ErrHLSSessionNotFound indicates the requested HLS session does not exist.
	ErrHLSSessionNotFound = errors.New("hls session not found")

	// ErrHLSSessionFailed indicates the HLS session is in a failed state.
	ErrHLSSessionFailed = errors.New("hls session failed")

	// ErrViewerNotFound indicates the HLS viewer ID is not registered.
	ErrViewerNotFound = errors.New("viewer not found")

	// ErrDuplicateRecording indicates a recording with this name/source already exists.
	ErrDuplicateRecording = errors.New("duplicate recording")

	// ErrInputInCooldown indicates the input recently failed and is in cooldown period.
	ErrInputInCooldown = errors.New("input in cooldown after failure")
)

// RelayError provides rich context for relay operation failures.
// It wraps an underlying error with operation details.
type RelayError struct {
	Op        string // Operation that failed (e.g., "start", "stop", "connect")
	InputURL  string // Input URL involved, if applicable
	OutputURL string // Output URL involved, if applicable
	Err       error  // Underlying error
}

func (e *RelayError) Error() string {
	if e.OutputURL != "" {
		return fmt.Sprintf("%s relay [%s → %s]: %v", e.Op, e.InputURL, e.OutputURL, e.Err)
	}
	if e.InputURL != "" {
		return fmt.Sprintf("%s relay [%s]: %v", e.Op, e.InputURL, e.Err)
	}
	return fmt.Sprintf("%s relay: %v", e.Op, e.Err)
}

func (e *RelayError) Unwrap() error { return e.Err }

// NewRelayError creates a RelayError with the given operation and input URL.
func NewRelayError(op, inputURL string, err error) *RelayError {
	return &RelayError{Op: op, InputURL: inputURL, Err: err}
}

// NewOutputRelayError creates a RelayError for output operations.
func NewOutputRelayError(op, inputURL, outputURL string, err error) *RelayError {
	return &RelayError{Op: op, InputURL: inputURL, OutputURL: outputURL, Err: err}
}

// FFmpegProcessError provides details about FFmpeg process failures.
// Named FFmpegProcessError to avoid conflict with FFmpegError status constant.
type FFmpegProcessError struct {
	Command  string   // FFmpeg command that was executed
	Args     []string // Arguments passed to FFmpeg
	ExitCode int      // Exit code, if available
	Output   string   // Last N lines of FFmpeg output
	Err      error    // Underlying error
}

func (e *FFmpegProcessError) Error() string {
	if e.ExitCode != 0 {
		return fmt.Sprintf("ffmpeg exited with code %d: %v", e.ExitCode, e.Err)
	}
	return fmt.Sprintf("ffmpeg failed: %v", e.Err)
}

func (e *FFmpegProcessError) Unwrap() error { return e.Err }

// ValidationError indicates invalid user input.
type ValidationError struct {
	Field   string // Field that failed validation
	Value   string // Invalid value (may be truncated for security)
	Message string // Human-readable description
}

func (e *ValidationError) Error() string {
	if e.Value != "" {
		return fmt.Sprintf("invalid %s %q: %s", e.Field, e.Value, e.Message)
	}
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Message)
}

// Is implements error matching for ValidationError, matching ErrInvalidInput.
func (e *ValidationError) Is(target error) bool {
	return target == ErrInvalidInput || target == ErrInvalidOutput
}

// NewValidationError creates a ValidationError.
func NewValidationError(field, value, message string) *ValidationError {
	// Truncate value to prevent log injection with long malicious inputs
	if len(value) > 100 {
		value = value[:100] + "..."
	}
	return &ValidationError{Field: field, Value: value, Message: message}
}
